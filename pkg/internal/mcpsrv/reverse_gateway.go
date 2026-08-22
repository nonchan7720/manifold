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

	// lazyBuildMu serializes ResolveServer's lazy first-build path (see
	// ResolveServer) so concurrent first accesses for a brand-new
	// identityKey build the base server once, not once per goroutine.
	lazyBuildMu sync.Mutex
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

// HandleAppUp connects to a newly-up tab's WebMCP MCP server over a single
// EdgeTransport built from sender/incoming, then binds that one connection
// under every identityKey in identityKeys (see the "ペアリングのプロファイル
// 対応" section of docs/design/webmcp-reverse-gateway-phase2.ja.md: an edge
// token may carry several profile bindings, all reaching the same tab).
func (g *ReverseGateway) HandleAppUp(
	ctx context.Context,
	identityKeys []domainedge.IdentityKey,
	origin, appSession, connID string,
	sender EdgeFrameSender,
	incoming <-chan json.RawMessage,
) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/ReverseGateway/HandleAppUp")
	defer func() { trace.EndSpan(ctx, rErr) }()

	srvCfg, ok := g.byOrigin[origin]
	if !ok {
		return fmt.Errorf("origin %q is not declared in any reverse mcpServers entry", origin)
	}

	transport := &EdgeTransport{
		Origin:     origin,
		AppSession: appSession,
		Sender:     sender,
		Incoming:   incoming,
	}

	// ToolListChangedHandler runs on the SDK's read goroutine and can fire
	// before client.Connect below returns (e.g. the page sends
	// notifications/tools/list_changed right after initialize); guard session
	// with a mutex instead of relying on happens-before across goroutines.
	var (
		sessionMu sync.Mutex
		session   *mcp.ClientSession
	)
	currentSession := func() *mcp.ClientSession {
		sessionMu.Lock()
		defer sessionMu.Unlock()
		return session
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "manifold", Version: version.Version},
		&mcp.ClientOptions{
			ToolListChangedHandler: func(ctx context.Context, _ *mcp.ToolListChangedRequest) {
				live := currentSession()
				if live == nil {
					return
				}
				for _, identityKey := range identityKeys {
					binding := domainedge.Binding{
						IdentityKey: identityKey,
						Origin:      origin,
						AppSession:  appSession,
						ConnID:      connID,
					}
					if err := g.rebuildUserServer(ctx, srvCfg.Name, binding, live); err != nil {
						slog.ErrorContext(ctx, "failed to rebuild reverse tool server",
							slog.String("origin", origin), slog.Any("error", err))
					}
				}
			},
		},
	)
	connected, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect to app %s: %w", origin, err)
	}
	sessionMu.Lock()
	session = connected
	sessionMu.Unlock()

	// Two passes: build every identityKey's server before binding any of
	// them, so a failure partway through leaves the registry untouched
	// instead of some identityKeys bound to a connection the other branch is
	// about to close.
	bindings := make([]domainedge.Binding, 0, len(identityKeys))
	for _, identityKey := range identityKeys {
		binding := domainedge.Binding{
			IdentityKey: identityKey,
			Origin:      origin,
			AppSession:  appSession,
			ConnID:      connID,
		}
		if err := g.rebuildUserServer(ctx, srvCfg.Name, binding, connected); err != nil {
			connected.Close()
			return fmt.Errorf("initialize tool server for app %s: %w", origin, err)
		}
		bindings = append(bindings, binding)
	}
	for _, binding := range bindings {
		previous, hadPrevious := g.registry.Bind(ctx, binding, connected)
		if hadPrevious {
			closeSessionHandle(previous)
		}
	}
	return nil
}

// HandleAppDown tears down (origin, appSession) for every identityKey in
// identityKeys, for whichever of them still match the currently bound
// generation. The per-user servers built from their tools/list are left in
// place so tool calls report tabNotConnectedMessage instead of "tool not
// found". Every identityKey shares the same underlying session (see
// HandleAppUp), so it is closed once even though it may be unbound from
// several registry entries.
func (g *ReverseGateway) HandleAppDown(
	ctx context.Context,
	identityKeys []domainedge.IdentityKey,
	origin, appSession string,
) {
	handles := make([]any, 0, len(identityKeys))
	for _, identityKey := range identityKeys {
		if handle, ok := g.registry.Unbind(ctx, identityKey, origin, appSession); ok {
			handles = append(handles, handle)
		}
	}
	closeUniqueHandles(handles, closeSessionHandle)
}

// DropConnection tears down every binding owned by connID (edge WebSocket
// disconnect). Several identityKeys can share one underlying session (see
// HandleAppUp), so handles are deduped the same way as HandleAppDown before
// closing.
func (g *ReverseGateway) DropConnection(ctx context.Context, connID string) {
	dropped := g.registry.DropConnection(ctx, connID)
	handles := make([]any, len(dropped))
	for i, d := range dropped {
		handles[i] = d.Handle
	}
	closeUniqueHandles(handles, closeSessionHandle)
}

// closeUniqueHandles closes each distinct handle in handles at most once.
func closeUniqueHandles(handles []any, closeHandle func(any)) {
	closed := map[any]bool{}
	for _, handle := range handles {
		if closed[handle] {
			continue
		}
		closed[handle] = true
		closeHandle(handle)
	}
}

// ResolveServer returns the current per-user mcp.Server for the named
// reverse mcpServers entry, keyed by the identityKey mcpAuthMiddleware stored
// in ctx (domainedge.WithIdentityKey) — static pairing always sets
// domainedge.StaticIdentityKey there; remote pairing sets whatever the
// server's identity profile derived from the request.
//
// remote has no identityKey to build ahead of a request (unlike static,
// which Init pre-builds), so the first call for a given (identityKey, origin)
// lazily builds the base server (create_pairing_code only, no tab bound
// yet) — the same shape Init produces for static — so create_pairing_code
// is always reachable even for a brand-new user.
func (g *ReverseGateway) ResolveServer(ctx context.Context, name string) (*mcp.Server, error) {
	srvCfg, ok := g.byName[name]
	if !ok {
		return nil, fmt.Errorf("not found mcp server: %s", name)
	}
	identityKey, ok := domainedge.IdentityKeyFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("mcpsrv: no identityKey in request context for server %s", name)
	}

	key := userServerKey(identityKey, srvCfg.Origin)
	if srv, ok := g.lookupUserServer(key); ok {
		return srv, nil
	}

	g.lazyBuildMu.Lock()
	defer g.lazyBuildMu.Unlock()
	if srv, ok := g.lookupUserServer(key); ok {
		return srv, nil
	}
	binding := domainedge.Binding{IdentityKey: identityKey, Origin: srvCfg.Origin}
	if err := g.rebuildUserServer(ctx, name, binding, nil); err != nil {
		return nil, fmt.Errorf("lazily initialize reverse tool server %s: %w", name, err)
	}
	srv, ok := g.lookupUserServer(key)
	if !ok {
		return nil, fmt.Errorf("not found mcp server: %s", name)
	}
	return srv, nil
}

func (g *ReverseGateway) lookupUserServer(key string) (*mcp.Server, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	srv, ok := g.userServers[key]
	return srv, ok
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
