package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/nonchan7720/manifold/pkg/version"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type MCPServer struct {
	servers config.Servers

	srv            *mcp.Server
	appSrv         map[string]*mcp.Server
	backendClients map[string]*MCPBackendClient
	a2aClients     map[string]*A2ABackendClient
	// serviceAgents は mcpServers.<name>.agents にぶら下げたエージェントを
	// サービス名ごとに保持する。
	serviceAgents map[string]*serviceAgents

	// mu guards openAPIStates, refreshCancel and refreshRejectOn, which the
	// spec refresh goroutines touch concurrently with request handling.
	mu            sync.Mutex
	openAPIStates map[string]*openAPIServerState
	refreshCancel context.CancelFunc
	refreshWG     sync.WaitGroup
	// refreshRejectOn is gateway.specRefresh.rejectOn, set by StartSpecRefresh.
	refreshRejectOn string

	mediaUploader *storage.ContentManagementService

	middlewareFn func(name string) []mcp.Middleware
	toolCache    *ToolCache
	auditLogger  *AuditLogger
	// toolSearchCfg は gateway.toolSearch（WithToolSearchConfig で設定、未設定なら無効）。
	toolSearchCfg config.ToolSearchConfig
	// authzKey は authz の判定入力（主体・bypass）のキャッシュキー（WithAuthzCacheKeyer）。
	authzKey AuthzCacheKeyer

	meterProvider metric.MeterProvider
	metrics       *specRefreshMetrics
}

// Option configures optional behavior of a MCPServer built by NewMCPServer.
type Option func(*MCPServer)

// WithServerMiddleware makes Init apply fn(name)'s middlewares to every
// per-backend *mcp.Server it creates, right after construction.
func WithServerMiddleware(fn func(name string) []mcp.Middleware) Option {
	return func(s *MCPServer) { s.middlewareFn = fn }
}

// WithToolCache makes servers with mcpServers.<name>.cache store their
// results in cache. Without it, cache settings are ignored.
func WithToolCache(cache *ToolCache) Option {
	return func(s *MCPServer) { s.toolCache = cache }
}

// WithToolSearchConfig sets gateway.toolSearch for every server. Without it,
// or with cfg.Enabled false, tool_search is off.
func WithToolSearchConfig(cfg config.ToolSearchConfig) Option {
	return func(s *MCPServer) { s.toolSearchCfg = cfg }
}

// WithAuthzCacheKeyer sets the keyer of the authz middlewares WithServerMiddleware
// installs (NewAuthzCacheKeyer), so tool_search can key its index cache by the
// authz principal. Without it, tool_search doesn't cache when authz is on.
func WithAuthzCacheKeyer(k AuthzCacheKeyer) Option {
	return func(s *MCPServer) { s.authzKey = k }
}

// WithAuditLogger records every tools/call on every server to l.
func WithAuditLogger(l *AuditLogger) Option {
	return func(s *MCPServer) { s.auditLogger = l }
}

// WithMeterProvider records the spec refresh metrics to mp instead of the
// global MeterProvider (otel.GetMeterProvider).
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(s *MCPServer) { s.meterProvider = mp }
}

func NewMCPServer(
	servers config.Servers,
	mediaUploader *storage.ContentManagementService,
	opts ...Option,
) *MCPServer {
	s := &MCPServer{
		servers: servers,
		srv: mcp.NewServer(
			&mcp.Implementation{Name: "manifold", Version: version.MarkVersion},
			&mcp.ServerOptions{},
		),
		appSrv:         map[string]*mcp.Server{},
		backendClients: map[string]*MCPBackendClient{},
		a2aClients:     map[string]*A2ABackendClient{},
		serviceAgents:  map[string]*serviceAgents{},
		openAPIStates:  map[string]*openAPIServerState{},
		mediaUploader:  mediaUploader,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.meterProvider == nil {
		s.meterProvider = otel.GetMeterProvider()
	}
	s.metrics = newSpecRefreshMetrics(s.meterProvider)
	return s
}

// registerOpenAPIServer builds and stores the openAPIServerState for an
// OpenAPI-mode server (file, URL, or configmap:// spec). A tools.file
// (generated catalog) failure still aborts startup, since it means the
// checked-in artifact is stale or missing and an operator needs to
// regenerate it. Any other spec fetch/parse failure starts the server with
// zero tools instead: the state is still recorded in openAPIStates so the
// regular spec-refresh cycle (refreshServer) can pick up the spec once it
// becomes available.
func (s *MCPServer) registerOpenAPIServer(
	ctx context.Context, name string, server *config.Server, srv *mcp.Server,
) error {
	register, toolInfos, err := registerAPI(
		ctx,
		server.Spec,
		server.BaseURL,
		server.ExtraHeaders,
		srv,
		s.mediaUploader,
		registerOpenAPIOptions(server)...)
	if err != nil {
		if server.GeneratedToolsFile() != "" {
			return fmt.Errorf("server %q: %w", name, err)
		}
		slog.WarnContext(ctx, "openapi spec fetch or parse failed; starting server with no tools",
			slog.String("server", name), slog.Any("error", err))
	}
	state := &openAPIServerState{srv: srv, cfg: server}
	if register != nil {
		// A fetch failure above is transient (the refresh loop retries), but
		// a catalog without a usable base URL is a configuration error: every
		// tools/call would fail, so refuse to start rather than serve it.
		if err := checkCatalogBaseURL(register); err != nil {
			return fmt.Errorf("server %q: %w", name, err)
		}
		state.adopt(register, toolInfos)
	}
	s.openAPIStates[name] = state
	return nil
}

// errBaseURLUnresolved reports a catalog whose tools have no absolute base
// URL to call: mcpServers.<name>.baseURL is unset and the spec gave nothing
// to derive one from (no servers / host entry, or only a relative one in a
// spec that was not fetched over http(s)).
var errBaseURLUnresolved = errors.New(
	"baseURL is not set and could not be derived from the spec " +
		"(it has no absolute servers/host entry and was not fetched over http(s)); " +
		"set mcpServers.<name>.baseURL",
)

// checkCatalogBaseURL returns errBaseURLUnresolved unless register's base
// URL is an absolute http(s) URL with a host (so "https:///api" or a bare
// "http://" are rejected too).
func checkCatalogBaseURL(register *MCPToolRegistry) error {
	baseURL := register.BaseURL()
	u, err := url.Parse(baseURL)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return nil
	}
	return fmt.Errorf("%w (derived %q)", errBaseURLUnresolved, baseURL)
}

func (s *MCPServer) Init(ctx context.Context) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPServer/Init")
	defer func() { trace.EndSpan(ctx, rErr) }()

	for name, server := range s.servers {
		if server.IsReverseBackend() {
			// reverse サーバーは mcpsrv.ReverseGateway が別途、identityKey ごとの
			// per-user mcp.Server を解決する。MCPServer 自身は appSrv/backendClients
			// のどちらにも登録しない。
			continue
		}

		srvOpts := &mcp.ServerOptions{}
		passthrough := server.IsMCPBackend() || server.IsA2ABackend()
		switch {
		case passthrough:
			// MCP / A2A バックエンドはツールを登録せず毎回転送するため、
			// tools capability の広告を明示する（listChanged はゲートウェイが
			// バックエンドの通知を転送しないため広告しない）。
			srvOpts.Capabilities = &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}
		case server.HasAgents():
			// エージェントをぶら下げた OpenAPI モードのサーバー。Capabilities を
			// 指定すると SDK は既定の広告（logging と、ツール登録時の
			// tools.listChanged）を足さなくなる。spec リフレッシュが
			// notifications/tools/list_changed を送るため、SDK の既定と同じ
			// logging と listChanged: true を明示する。これで spec のツールが 0 個でも
			// エージェントのツールを返せるよう tools capability も保証される。
			srvOpts.Capabilities = &mcp.ServerCapabilities{
				//nolint:staticcheck // SDK の既定の広告と揃えるため（logging は非推奨だが有効）
				Logging: &mcp.LoggingCapabilities{},
				Tools:   &mcp.ToolCapabilities{ListChanged: true},
			}
		}
		srv := mcp.NewServer(
			&mcp.Implementation{Name: name, Version: version.MarkVersion},
			srvOpts,
		)
		if server.IsMCPBackend() {
			// MCP バックエンドモード: 遅延接続クライアントを登録し、
			// tools/list・tools/call はバックエンドへ毎回転送する。
			// パススルーは authz ミドルウェアより先に追加して内側に置く
			// （サービスエージェントのミドルウェアはその次、authz の前）。
			bc := &MCPBackendClient{name: name, cfg: server}
			s.backendClients[name] = bc
			srv.AddReceivingMiddleware(newBackendPassthroughMiddleware(bc))
		}
		if server.IsA2ABackend() {
			// A2A エージェント（agents ディレクティブ由来）:
			// Agent Card のスキルを tools/list で返し、
			// tools/call を message/send へ転送する。Card は起動時に取得を試み、
			// 失敗しても最初のリクエストで取り直す。
			ac := NewA2ABackendClient(name, server, s.mediaService())
			s.a2aClients[name] = ac
			srv.AddReceivingMiddleware(newBackendPassthroughMiddleware(ac))
			if _, err := ac.EnsureCard(ctx); err != nil {
				slog.WarnContext(ctx, "a2a agent card fetch failed; retrying on first request",
					slog.String("agent", name), slog.Any("error", err))
			}
		}
		if server.HasAgents() {
			// mcpServers.<name>.agents: サービス自身のツールの後ろに
			// <agent>__<skill> のツールを足し、その tools/call を message/send へ
			// 転送する。バックエンドのパススルーより後（= 外側。OpenAPI モードでは
			// SDK 自身の tools/list ハンドラの外側）、authz より先（= 内側）に追加する。
			// Card は起動時に取得を試み、失敗しても最初のリクエストで取り直す。
			sa := newServiceAgents(name, server.Agents, s.mediaService())
			s.serviceAgents[name] = sa
			srv.AddReceivingMiddleware(newServiceAgentsMiddleware(sa))
			sa.ensureCards(ctx)
		}
		var authzMiddlewares []mcp.Middleware
		if s.middlewareFn != nil {
			authzMiddlewares = s.middlewareFn(name)
		}
		srv.AddReceivingMiddleware(
			ServerToolMiddlewares(
				name,
				server,
				authzMiddlewares,
				s.authzKey,
				s.toolCache,
				s.auditLogger,
				s.toolSearchCfg,
			)...,
		)

		if !passthrough {
			// OpenAPI モード
			if err := s.registerOpenAPIServer(ctx, name, server, srv); err != nil {
				return err
			}
		}
		s.appSrv[name] = srv
	}
	return nil
}

// Server は指定された名前の MCP サーバーを返す。
func (s *MCPServer) Server(name string) (*mcp.Server, error) {
	if srv, ok := s.appSrv[name]; ok {
		return srv, nil
	}
	return nil, fmt.Errorf("not found mcp server: %s", name)
}

// BackendClient は指定された名前の MCP バックエンドクライアントを返す。
// MCP バックエンドモードのサーバーにのみ存在する。
func (s *MCPServer) BackendClient(name string) (*MCPBackendClient, bool) {
	bc, ok := s.backendClients[name]
	return bc, ok
}

// A2AClient は指定された名前の A2A エージェントクライアントを返す。
// agents ディレクティブ由来のサーバーにのみ存在する（mcpServers.<name>.agents に
// ぶら下げたエージェントは serviceAgents が保持する）。
func (s *MCPServer) A2AClient(name string) (*A2ABackendClient, bool) {
	ac, ok := s.a2aClients[name]
	return ac, ok
}

// mediaService はツール結果のバイナリをアップロードする MediaService を返す。
// 未設定なら noop アップローダーを返す。
func (s *MCPServer) mediaService() storage.MediaService {
	if s.mediaUploader == nil {
		return storage.NewNoopUploader()
	}
	return s.mediaUploader
}

// ToolCatalog returns the full (name, description) tool list for name,
// independent of any per-caller tools/list authz filtering (see
// authz_middleware.go): OpenAPI mode reads it from openAPIStates, MCP
// backend mode connects lazily and queries the backend's tools/list on
// every call. Reverse-transport servers have no catalog here — their tools
// only exist per-identityKey after a browser connects — and are reported as
// "not found" like any other unknown name.
func (s *MCPServer) ToolCatalog(ctx context.Context, name string) ([]ToolInfo, error) {
	s.mu.Lock()
	state, hasOpenAPI := s.openAPIStates[name]
	var infos []ToolInfo
	if hasOpenAPI {
		infos = slices.Clone(state.toolInfos)
	}
	s.mu.Unlock()

	if !hasOpenAPI {
		var err error
		if infos, err = s.backendToolInfos(ctx, name); err != nil {
			return nil, err
		}
	}

	// mcpServers.<name>.agents にぶら下げたエージェントのツールをサービス自身の
	// ツールの後ろに足す（tools/list と同じ並び）。サービスのツールと同名の
	// エージェントのツールは、tools/list と同じくサービスを優先して外す。
	if sa, ok := s.serviceAgents[name]; ok {
		infos = append(infos, sa.dropCollidingInfos(ctx, infos, sa.listToolInfos(ctx))...)
	}
	// tools.include / exclude / overrides を tools/list と同じく反映する。
	if server, ok := s.servers[name]; ok && server != nil {
		infos = newToolFilter(server.Tools).applyInfos(infos)
	}
	return infos, nil
}

// backendToolInfos は OpenAPI 以外のサーバー（MCP バックエンド、または agents
// ディレクティブ由来の A2A エージェント）自身のツール一覧を返す。
// mcpServers.<name>.agents のツールは含まない。
func (s *MCPServer) backendToolInfos(ctx context.Context, name string) ([]ToolInfo, error) {
	if bc, ok := s.backendClients[name]; ok {
		return bc.ListToolInfos(ctx)
	}
	if ac, ok := s.a2aClients[name]; ok {
		return ac.ListToolInfos(ctx)
	}
	return nil, fmt.Errorf("not found mcp server: %s", name)
}

// Close は spec リフレッシュの goroutine を停止し、全バックエンドクライアントの接続を閉じる。
func (s *MCPServer) Close() {
	s.stopSpecRefresh()
	for _, bc := range s.backendClients {
		bc.Close()
	}
	for _, ac := range s.a2aClients {
		ac.Close()
	}
	for _, sa := range s.serviceAgents {
		sa.close()
	}
}

func registerOpenAPIOptions(server *config.Server) []RegisterOpenAPIOption {
	opts := []RegisterOpenAPIOption{
		WithAuth(server.AuthValue),
		WithOAuth2(server.OAuth2),
		WithTokenExchange(server.TokenExchange),
	}
	if file := server.GeneratedToolsFile(); file != "" {
		opts = append(opts, WithGeneratedToolsFile(file))
	}
	return opts
}

// registerAPI builds the tools of an OpenAPI mode server and registers them on
// srv, returning the registry they were built into and the registered tools.
func registerAPI(
	ctx context.Context,
	spec, baseURL string,
	headers map[string]string,
	srv *mcp.Server,
	mediaUploader storage.MediaService,
	opts ...RegisterOpenAPIOption,
) (*MCPToolRegistry, []ToolInfo, error) {
	// OpenAPI モード: 既存ロジック
	register, err := RegisterOpenAPI(ctx, spec, baseURL, headers, opts...)
	if err != nil {
		return nil, nil, err
	}
	return register, attachTools(srv, register, mediaUploader), nil
}

func attachTools(
	srv *mcp.Server,
	register *MCPToolRegistry,
	mediaUploader storage.MediaService,
) []ToolInfo {
	tools := register.ListTools()
	infos := make([]ToolInfo, 0, len(tools))
	for _, tool := range tools {
		infos = append(infos, ToolInfo{
			Name:        tool.tool.Name,
			Summary:     tool.summary,
			Description: tool.description,
		})
		srv.AddTool(
			&tool.tool,
			func(ctx context.Context, ctr *mcp.CallToolRequest) (res *mcp.CallToolResult, rErr error) {
				spanName := fmt.Sprintf("mcpsrv/MCPServer/Handler/%s", ctr.Params.Name)
				ctx = trace.StartSpan(ctx, spanName, attribute.String("tool-name", ctr.Params.Name))
				defer func() {
					if res.IsError {
						rErr = errors.Join(rErr, res.GetError())
					}
					trace.EndSpan(ctx, rErr)
				}()
				slog.InfoContext(ctx, "call tool", slog.String("tool-name", ctr.Params.Name))

				var input map[string]any
				if err := json.Unmarshal(ctr.Params.Arguments, &input); err != nil {
					resp := &mcp.CallToolResult{}
					resp.SetError(err)
					return resp, nil
				}
				var result mcp.CallToolResult
				resp, contentType, err := tool.handler(ctx, input)
				if err != nil {
					result.SetError(err)
					return &result, nil
				}
				content, err := generateContent(ctx, contentType, resp, mediaUploader)
				if err != nil {
					result.SetError(err)
				} else {
					result.Content = content
					if json.Valid(resp) {
						result.StructuredContent = json.RawMessage(resp)
					}
				}
				return &result, nil
			},
		)
	}
	return infos
}

// resourceLinkDescription は resource_link の説明文を組み立てる。
//
// mimeType フィールドは、受け手（Claude Code 等）が resource_link を
// `[Resource link: {name}] {uri} ({description})` というテキストへ変換する過程で失われる。
// 説明文は変換後も残るため、Content-Type をここにも書いておく。
func resourceLinkDescription(contentType string) string {
	return fmt.Sprintf(
		"Content-Type: %s. When using the data, please use the accessible URL",
		contentType,
	)
}

// newResourceLink は実体をアップロードし、その参照を表す resource_link を返す。
func newResourceLink(
	ctx context.Context,
	data []byte,
	contentType string,
	mediaService storage.MediaService,
) (mcp.Content, error) {
	id, url, err := mediaService.SaveContent(ctx, data, contentType)
	if err != nil {
		return nil, err
	}
	return &mcp.ResourceLink{
		URI:         url,
		Name:        id,
		MIMEType:    contentType,
		Description: resourceLinkDescription(contentType),
	}, nil
}

func generateContent(
	ctx context.Context,
	contentType string,
	data []byte,
	mediaService storage.MediaService,
) ([]mcp.Content, error) {
	// 上流が octet-stream しか返さない場合に備え、実体から型を判定し直す。
	// 判定できた型は振り分け（image/audio/その他）と resource_link の両方に使う。
	contentType = storage.ResolveContentType(contentType, data)
	baseType := strings.SplitN(contentType, ";", 2)[0]
	baseType = strings.TrimSpace(baseType)
	isEnabled := mediaService.Enabled()
	switch {
	case oastomcptool.IsTextContentType(baseType):

		return []mcp.Content{
			&mcp.TextContent{
				Text: string(data),
			},
		}, nil
	case strings.HasPrefix(baseType, "image/"):
		if !isEnabled {
			return []mcp.Content{&mcp.ImageContent{Data: data, MIMEType: contentType}}, nil
		}
		content, err := newResourceLink(ctx, data, contentType, mediaService)
		if err != nil {
			return nil, err
		}
		return []mcp.Content{content}, nil
	case strings.HasPrefix(baseType, "audio/"):
		if !isEnabled {
			return []mcp.Content{&mcp.AudioContent{Data: data, MIMEType: contentType}}, nil
		}
		content, err := newResourceLink(ctx, data, contentType, mediaService)
		if err != nil {
			return nil, err
		}
		return []mcp.Content{content}, nil
	default:
		if !isEnabled {
			return []mcp.Content{&mcp.TextContent{Text: string(data)}}, nil
		}
		content, err := newResourceLink(ctx, data, contentType, mediaService)
		if err != nil {
			return nil, err
		}
		return []mcp.Content{content}, nil
	}
}
