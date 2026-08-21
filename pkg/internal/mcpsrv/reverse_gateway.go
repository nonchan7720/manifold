package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
	"github.com/nonchan7720/manifold/pkg/version"
)

const createPairingCodeToolName = "create_pairing_code"

// ErrIdentityDerivationNotImplemented is returned when resolving a reverse
// tool call would require deriving identityKey from the identities profile
// system (edge.pairing.type=remote or edge.auth=forwardAuth), which Phase 1
// does not implement.
var ErrIdentityDerivationNotImplemented = errors.New(
	"mcpsrv: identity derivation for this edge auth mode is not implemented yet; " +
		"only edge.pairing.type=static is supported",
)

// IdentityKeyForRequest derives the identityKey used to resolve a reverse
// tool call. Only edge.pairing.type=static is implemented: the identityKey is
// always the fixed StaticIdentityKey. Deriving identityKey from the
// identities profile system (remote pairing or forwardAuth) is Phase 2/3.
func IdentityKeyForRequest(edgeCfg config.EdgeConfig) (domainedge.IdentityKey, error) {
	if edgeCfg.WithDefaults().IsStaticPairing() {
		return domainedge.StaticIdentityKey, nil
	}
	return "", ErrIdentityDerivationNotImplemented
}

// tabNotConnectedMessage is the tool-error text an agent can pass straight
// through to the user (see the エラー体系 table in
// docs/design/webmcp-reverse-gateway.ja.md).
func tabNotConnectedMessage(origin string) string {
	return fmt.Sprintf(
		"対象アプリのタブが開かれていません。%s を開いたままにするようユーザーに案内してください",
		origin,
	)
}

func userServerKey(identityKey domainedge.IdentityKey, origin string) string {
	return string(identityKey) + "|" + origin
}

// ReverseGateway builds and resolves the per-(identityKey, origin) mcp.Server
// exposed for reverse-transport mcpServers. Each server always carries the
// create_pairing_code tool; once a tab connects (HandleAppUp), its
// WebMCP-declared tools are merged in and routed to the live session.
type ReverseGateway struct {
	registry domainedge.Registry
	pairing  *edgeservices.PairingService
	edgeCfg  config.EdgeConfig

	byName   map[string]*config.Server
	byOrigin map[string]*config.Server

	mu          sync.Mutex
	userServers map[string]*mcp.Server
}

// NewReverseGateway creates a ReverseGateway for the reverse-transport
// entries of servers. Non-reverse entries are ignored.
func NewReverseGateway(
	registry domainedge.Registry,
	pairing *edgeservices.PairingService,
	edgeCfg config.EdgeConfig,
	servers config.Servers,
) *ReverseGateway {
	g := &ReverseGateway{
		registry:    registry,
		pairing:     pairing,
		edgeCfg:     edgeCfg.WithDefaults(),
		byName:      map[string]*config.Server{},
		byOrigin:    map[string]*config.Server{},
		userServers: map[string]*mcp.Server{},
	}
	for name, srv := range servers {
		if !srv.IsReverseBackend() {
			continue
		}
		g.byName[name] = srv
		g.byOrigin[srv.Origin] = srv
	}
	return g
}

// Init eagerly builds the base per-user server (create_pairing_code only, no
// tab connected yet) for every deployment where identityKey is knowable ahead
// of any request, i.e. edge.pairing.type=static.
func (g *ReverseGateway) Init(ctx context.Context) {
	if !g.edgeCfg.IsStaticPairing() {
		return
	}
	for name, srv := range g.byName {
		binding := domainedge.Binding{IdentityKey: domainedge.StaticIdentityKey, Origin: srv.Origin}
		if err := g.rebuildUserServer(ctx, name, binding, nil); err != nil {
			slog.ErrorContext(ctx, "failed to initialize reverse tool server",
				slog.String("server", name), slog.Any("error", err))
		}
	}
}

// HasReverseServers reports whether any reverse-transport mcpServers entry
// was declared.
func (g *ReverseGateway) HasReverseServers() bool {
	return len(g.byName) > 0
}

// ServerNames returns the reverse-transport server names.
func (g *ReverseGateway) ServerNames() []string {
	names := make([]string, 0, len(g.byName))
	for name := range g.byName {
		names = append(names, name)
	}
	return names
}

// Origins returns the configured reverse origins, for the edge WS "ready" frame.
func (g *ReverseGateway) Origins() []string {
	origins := make([]string, 0, len(g.byOrigin))
	for origin := range g.byOrigin {
		origins = append(origins, origin)
	}
	return origins
}

// HasServer reports whether name is a declared reverse-transport server.
func (g *ReverseGateway) HasServer(name string) bool {
	_, ok := g.byName[name]
	return ok
}

// IsKnownOrigin reports whether origin belongs to a declared reverse server.
func (g *ReverseGateway) IsKnownOrigin(origin string) bool {
	_, ok := g.byOrigin[origin]
	return ok
}

// HandleAppUp connects to a newly-up tab's WebMCP MCP server over an
// EdgeTransport built from sender/incoming, then builds (or rebuilds) the
// per-user server exposing its tools alongside create_pairing_code.
func (g *ReverseGateway) HandleAppUp(
	ctx context.Context,
	binding domainedge.Binding,
	sender EdgeFrameSender,
	incoming <-chan json.RawMessage,
) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/ReverseGateway/HandleAppUp")
	defer func() { trace.EndSpan(ctx, rErr) }()

	srvCfg, ok := g.byOrigin[binding.Origin]
	if !ok {
		return fmt.Errorf(
			"origin %q is not declared in any reverse mcpServers entry",
			binding.Origin,
		)
	}

	transport := &EdgeTransport{
		Origin:     binding.Origin,
		AppSession: binding.AppSession,
		Sender:     sender,
		Incoming:   incoming,
	}

	var session *mcp.ClientSession
	client := mcp.NewClient(
		&mcp.Implementation{Name: "manifold", Version: version.Version},
		&mcp.ClientOptions{
			ToolListChangedHandler: func(ctx context.Context, _ *mcp.ToolListChangedRequest) {
				if err := g.rebuildUserServer(ctx, srvCfg.Name, binding, session); err != nil {
					slog.ErrorContext(ctx, "failed to rebuild reverse tool server",
						slog.String("origin", binding.Origin), slog.Any("error", err))
				}
			},
		},
	)
	connected, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect to app %s: %w", binding.Origin, err)
	}
	session = connected

	if err := g.rebuildUserServer(ctx, srvCfg.Name, binding, session); err != nil {
		session.Close()
		return fmt.Errorf("initialize tool server for app %s: %w", binding.Origin, err)
	}

	previous, hadPrevious := g.registry.Bind(ctx, binding, session)
	if hadPrevious {
		closeSessionHandle(previous)
	}
	return nil
}

// HandleAppDown tears down the binding's live session if appSession still
// matches the currently bound generation. The per-user server built from its
// tools/list is left in place so tool calls report tabNotConnectedMessage
// instead of "tool not found".
func (g *ReverseGateway) HandleAppDown(
	ctx context.Context,
	identityKey domainedge.IdentityKey,
	origin, appSession string,
) {
	handle, ok := g.registry.Unbind(ctx, identityKey, origin, appSession)
	if !ok {
		return
	}
	closeSessionHandle(handle)
}

// DropConnection tears down every binding owned by connID (edge WebSocket
// disconnect).
func (g *ReverseGateway) DropConnection(ctx context.Context, connID string) {
	for _, dropped := range g.registry.DropConnection(ctx, connID) {
		closeSessionHandle(dropped.Handle)
	}
}

// ResolveServer returns the current per-user mcp.Server for the named
// reverse mcpServers entry, deriving identityKey from edgeCfg.
func (g *ReverseGateway) ResolveServer(_ context.Context, name string) (*mcp.Server, error) {
	srvCfg, ok := g.byName[name]
	if !ok {
		return nil, fmt.Errorf("not found mcp server: %s", name)
	}
	identityKey, err := IdentityKeyForRequest(g.edgeCfg)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	srv, ok := g.userServers[userServerKey(identityKey, srvCfg.Origin)]
	g.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("not found mcp server: %s", name)
	}
	return srv, nil
}

// rebuildUserServer (re)builds the per-user server for binding, merging
// create_pairing_code with session's tools/list (session may be nil before
// any tab has ever connected).
func (g *ReverseGateway) rebuildUserServer(
	ctx context.Context,
	name string,
	binding domainedge.Binding,
	session *mcp.ClientSession,
) error {
	srv := mcp.NewServer(
		&mcp.Implementation{Name: name, Version: version.MarkVersion},
		&mcp.ServerOptions{},
	)
	g.registerPairingTool(srv, binding.IdentityKey)

	if session != nil {
		result, err := session.ListTools(ctx, nil)
		if err != nil {
			return fmt.Errorf("list tools for app %s: %w", binding.Origin, err)
		}
		RegisterSessionTools(srv, result.Tools, g.sessionResolver(binding))
	}

	g.mu.Lock()
	g.userServers[userServerKey(binding.IdentityKey, binding.Origin)] = srv
	g.mu.Unlock()
	return nil
}

// sessionResolver looks up the live session for binding at call time, rather
// than closing over the session available when the tool was registered, so
// that a call after the tab disconnects reports tabNotConnectedMessage.
func (g *ReverseGateway) sessionResolver(binding domainedge.Binding) SessionResolver {
	return func(ctx context.Context) (*mcp.ClientSession, error) {
		handle, ok := g.registry.Resolve(ctx, binding.IdentityKey, binding.Origin)
		if !ok {
			return nil, errors.New(tabNotConnectedMessage(binding.Origin))
		}
		session, ok := handle.(*mcp.ClientSession)
		if !ok || session == nil {
			return nil, errors.New(tabNotConnectedMessage(binding.Origin))
		}
		return session, nil
	}
}

func (g *ReverseGateway) registerPairingTool(srv *mcp.Server, identityKey domainedge.IdentityKey) {
	srv.AddTool(
		&mcp.Tool{
			Name: createPairingCodeToolName,
			Description: "Issue a short-lived pairing code so the user can link their browser " +
				"extension to this session, making the app's WebMCP tools callable.",
			InputSchema: map[string]any{"type": "object"},
		},
		func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result := &mcp.CallToolResult{}
			code, err := g.pairing.IssueCode(ctx, identityKey)
			if err != nil {
				result.SetError(err)
				return result, nil
			}
			result.Content = []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf(
					"Pairing code: %s (valid for 5 minutes, single use). "+
						"Ask the user to enter this code into the Manifold browser extension "+
						"to link their browser.",
					code,
				),
			}}
			return result, nil
		},
	)
}

func closeSessionHandle(handle any) {
	if session, ok := handle.(*mcp.ClientSession); ok && session != nil {
		_ = session.Close()
	}
}
