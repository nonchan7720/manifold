package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/version"
)

// ServerToolMiddlewares returns the tool middlewares every server gets, in
// the order AddReceivingMiddleware expects within one call (outermost first):
//
//	audit → authz (from authzMiddlewares) → cache → tool filter
//
// The tool filter sits right outside the backend so every outer layer sees
// the exposed names; the cache sits inside authz so a cached result is only
// returned to a caller allowed to call the tool; audit sits outside authz so
// denied calls are recorded too.
func ServerToolMiddlewares(
	name string,
	server *config.Server,
	authzMiddlewares []mcp.Middleware,
	cache *ToolCache,
	audit *AuditLogger,
) []mcp.Middleware {
	var (
		tools   *config.ToolsConfig
		cacheCf *config.CacheConfig
		service = name
	)
	if server != nil {
		tools, cacheCf, service = server.Tools, server.Cache, server.ServiceCode()
	}
	var out []mcp.Middleware
	if m := newAuditMiddleware(name, service, audit); m != nil {
		out = append(out, m)
	}
	out = append(out, authzMiddlewares...)
	if m := newToolCacheMiddleware(name, cacheCf, cache); m != nil {
		out = append(out, m)
	}
	if m := newToolFilterMiddleware(tools); m != nil {
		out = append(out, m)
	}
	return out
}

// memberDispatcher calls one server's full middleware chain (everything but
// tool search) directly, for the aggregated endpoint.
//
// The SDK's own handlers resolve the server from the request's session, so
// a request must carry a session of the member server: session is an
// in-memory session opened once at startup, whose client end (client)
// drains the notifications the server sends (e.g. tools/list_changed after
// a spec refresh). The caller's context and HTTP request extra are passed
// through unchanged, so the bearer token, authz headers and tracing behave as
// on the member's own /mcp/{server_name} endpoint.
type memberDispatcher struct {
	name    string
	handler mcp.MethodHandler
	session *mcp.ServerSession
	client  *mcp.ClientSession
}

func (m *memberDispatcher) close() {
	if m.client != nil {
		_ = m.client.Close()
	}
	if m.session != nil {
		_ = m.session.Close()
	}
}

func (m *memberDispatcher) listTools(
	ctx context.Context,
	extra *mcp.RequestExtra,
) ([]*mcp.Tool, error) {
	var (
		tools  []*mcp.Tool
		cursor string
	)
	for range maxToolSearchListPages {
		res, err := m.handler(ctx, authzMethodToolsList, &mcp.ListToolsRequest{
			Session: m.session,
			Params:  &mcp.ListToolsParams{Cursor: cursor},
			Extra:   extra,
		})
		if err != nil {
			return nil, err
		}
		result, ok := res.(*mcp.ListToolsResult)
		if !ok {
			return nil, fmt.Errorf("unexpected tools/list result %T", res)
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return tools, nil
}

func (m *memberDispatcher) callTool(
	ctx context.Context, params *mcp.CallToolParamsRaw, tool string, extra *mcp.RequestExtra,
) (mcp.Result, error) {
	copied := *params
	copied.Name = tool
	return m.handler(ctx, authzMethodToolsCall, &mcp.CallToolRequest{
		Session: m.session,
		Params:  &copied,
		Extra:   extra,
	})
}

// aggregator routes the aggregated endpoint's tools/list and tools/call to
// its member servers.
type aggregator struct {
	members   []*memberDispatcher
	byName    map[string]*memberDispatcher
	separator string
}

// listTools fans tools/list out to every member in parallel and returns the
// tools renamed <server><separator><tool>, in member order. A member that
// fails (backend down, or authz denying the whole list) is logged and left
// out instead of failing the aggregated list.
func (a *aggregator) listTools(ctx context.Context, extra *mcp.RequestExtra) []*mcp.Tool {
	results := make([][]*mcp.Tool, len(a.members))
	var wg sync.WaitGroup
	for i, m := range a.members {
		wg.Go(func() {
			tools, err := m.listTools(ctx, extra)
			if err != nil {
				slog.WarnContext(ctx, "aggregate: server skipped from tools/list",
					slog.String("server", m.name), slog.Any("error", err))
				return
			}
			renamed := make([]*mcp.Tool, len(tools))
			for j, tool := range tools {
				copied := *tool
				copied.Name = m.name + a.separator + tool.Name
				renamed[j] = &copied
			}
			results[i] = renamed
		})
	}
	wg.Wait()
	var out []*mcp.Tool
	for _, tools := range results {
		out = append(out, tools...)
	}
	if out == nil {
		out = []*mcp.Tool{}
	}
	return out
}

// route splits an aggregated tool name into its member and the member's own
// tool name. Member names never contain the separator (config validation),
// so the first separator ends the server name.
func (a *aggregator) route(name string) (*memberDispatcher, string, bool) {
	server, tool, found := strings.Cut(name, a.separator)
	if !found || tool == "" {
		return nil, "", false
	}
	m, ok := a.byName[server]
	return m, tool, ok
}

func (a *aggregator) middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodToolsList:
				res := &mcp.ListToolsResult{Tools: a.listTools(ctx, req.GetExtra())}
				normalizeCacheable(&res.Cacheable)
				return res, nil
			case authzMethodToolsCall:
				params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
				if !ok {
					return nil, &jsonrpc.Error{
						Code:    jsonrpc.CodeInvalidParams,
						Message: "invalid tools/call params",
					}
				}
				m, tool, ok := a.route(params.Name)
				if !ok {
					return nil, unknownToolError(params.Name)
				}
				return m.callTool(ctx, params, tool, req.GetExtra())
			default:
				return next(ctx, method, req)
			}
		}
	}
}

// NewAggregateServer builds the *mcp.Server behind the aggregated endpoint:
// its tools/list is the union of every member's (each tool renamed
// <server><separator><tool>), and its tools/call routes to the member the
// name points at, through that member's full middleware chain (tool filter,
// cache, authz, audit). search, when enabled, replaces the aggregated
// tools/list with search_tools / call_tool.
//
// Members must be servers Init registered (not reverse servers).
func (s *MCPServer) NewAggregateServer(
	ctx context.Context,
	members []string,
	separator string,
	search *config.ToolSearchConfig,
) (*mcp.Server, error) {
	agg := &aggregator{byName: map[string]*memberDispatcher{}, separator: separator}
	for _, name := range members {
		m, err := s.openMemberDispatcher(ctx, name)
		if err != nil {
			for _, opened := range agg.members {
				opened.close()
			}
			return nil, err
		}
		agg.members = append(agg.members, m)
		agg.byName[name] = m
	}

	srv := mcp.NewServer(
		&mcp.Implementation{Name: "manifold", Version: version.MarkVersion},
		&mcp.ServerOptions{
			Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		},
	)
	srv.AddReceivingMiddleware(agg.middleware())
	if m := newToolSearchMiddleware(search); m != nil {
		srv.AddReceivingMiddleware(m)
	}

	s.mu.Lock()
	s.aggregates = append(s.aggregates, agg)
	s.mu.Unlock()
	return srv, nil
}

func (s *MCPServer) openMemberDispatcher(
	ctx context.Context,
	name string,
) (*memberDispatcher, error) {
	srv, ok := s.appSrv[name]
	handler, hasHandler := s.dispatch[name]
	if !ok || !hasHandler {
		return nil, fmt.Errorf("aggregate: server %q is not available", name)
	}
	// セッションはサーバーの寿命と同じだけ保持するため、起動時の ctx の
	// キャンセルでは閉じない。
	ctx = context.WithoutCancel(ctx)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	session, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("aggregate: open session for %q: %w", name, err)
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "manifold-aggregate", Version: version.MarkVersion}, nil,
	)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = session.Close()
		return nil, errors.Join(fmt.Errorf("aggregate: initialize session for %q", name), err)
	}
	return &memberDispatcher{name: name, handler: handler, session: session, client: cs}, nil
}

// newDispatchCaptureMiddleware records the chain it wraps under name in
// s.dispatch, for NewAggregateServer. It must be added after every middleware
// the aggregated endpoint should go through, and before tool search.
func (s *MCPServer) newDispatchCaptureMiddleware(name string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		s.dispatch[name] = next
		return next
	}
}
