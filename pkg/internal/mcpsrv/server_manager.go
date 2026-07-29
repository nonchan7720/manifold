package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/toolsearch"
	"github.com/nonchan7720/manifold/pkg/version"
	"go.opentelemetry.io/otel/attribute"
)

type MCPServer struct {
	servers config.Servers

	srv            *mcp.Server
	appSrv         map[string]*mcp.Server
	backendClients map[string]*MCPBackendClient

	mediaUploader *storage.ContentManagementService

	catalog       *toolsearch.Catalog
	toolSearchCfg config.ToolSearchConfig
}

// MCPServerOption は NewMCPServer の任意設定を行うオプション関数。
type MCPServerOption func(*MCPServer)

// WithToolSearchConfig は tool_search 機能の閾値・デフォルト検索件数を設定する。
// 未指定の場合は config.ToolSearchConfig{}.WithDefaults() が適用される。
func WithToolSearchConfig(cfg config.ToolSearchConfig) MCPServerOption {
	return func(s *MCPServer) {
		s.toolSearchCfg = cfg.WithDefaults()
	}
}

func NewMCPServer(servers config.Servers, mediaUploader *storage.ContentManagementService, opts ...MCPServerOption) *MCPServer {
	s := &MCPServer{
		servers:        servers,
		srv:            mcp.NewServer(&mcp.Implementation{Name: "manifold", Version: version.MarkVersion}, &mcp.ServerOptions{}),
		appSrv:         map[string]*mcp.Server{},
		backendClients: map[string]*MCPBackendClient{},
		mediaUploader:  mediaUploader,
		catalog:        toolsearch.NewCatalog(),
		toolSearchCfg:  config.ToolSearchConfig{}.WithDefaults(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *MCPServer) Init(ctx context.Context) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPServer/Init")
	defer func() { trace.EndSpan(ctx, rErr) }()

	for name, server := range s.servers {
		srv := mcp.NewServer(&mcp.Implementation{Name: name, Version: version.MarkVersion}, &mcp.ServerOptions{})

		if server.IsMCPBackend() {
			// MCP バックエンドモード: 遅延接続のためクライアントを登録するのみ
			s.backendClients[name] = &MCPBackendClient{
				name:    name,
				cfg:     server,
				srv:     srv,
				catalog: s.catalog,
			}
		} else {
			// OpenAPI モード
			opts := []RegisterOpenAPIOption{
				WithAuth(server.AuthValue),
				WithOAuth2(server.OAuth2),
				WithTokenExchange(server.TokenExchange),
			}
			err := registerAPI(ctx, server.Spec, server.BaseURL, server.ExtraHeaders, srv, s.mediaUploader, name, s.catalog, opts...)
			if err != nil {
				return err
			}
		}

		registerToolSearch(srv, name, s.catalog, s.toolSearchCfg)
		srv.AddReceivingMiddleware(hideToolsMiddleware(name, s.catalog, s.toolSearchCfg))

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

// Close は全バックエンドクライアントの接続を閉じる。
func (s *MCPServer) Close() {
	for _, bc := range s.backendClients {
		bc.Close()
	}
}

func registerAPI(
	ctx context.Context,
	spec, baseURL string,
	headers map[string]string,
	srv *mcp.Server,
	mediaUploader storage.MediaService,
	serverName string,
	catalog *toolsearch.Catalog,
	opts ...RegisterOpenAPIOption,
) error {
	// OpenAPI モード: 既存ロジック
	register, err := RegisterOpenAPI(ctx, spec, baseURL, headers, opts...)
	if err != nil {
		return err
	}
	tools := register.ListTools()
	for _, tool := range tools {
		catalog.Add(serverName, toolsearch.ToolDef{
			Name:        tool.tool.Name,
			Description: tool.tool.Description,
			InputSchema: tool.tool.InputSchema,
		})
		srv.AddTool(&tool.tool, func(ctx context.Context, ctr *mcp.CallToolRequest) (res *mcp.CallToolResult, rErr error) {
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
		})
	}
	return nil
}

func generateContent(ctx context.Context, contentType string, data []byte, mediaService storage.MediaService) ([]mcp.Content, error) {
	baseType := strings.SplitN(contentType, ";", 2)[0]
	baseType = strings.TrimSpace(baseType)
	textContent := []string{
		"application/json",
		"application/xml",
		"application/yaml",
		// 非標準パターン
		"application/x-yaml",
		"application/yml",
	}
	isEnabled := mediaService.Enabled()
	switch {
	case strings.HasPrefix(baseType, "text/"),
		slices.Contains(textContent, baseType):

		return []mcp.Content{
			&mcp.TextContent{
				Text: string(data),
			},
		}, nil
	case strings.HasPrefix(baseType, "image/"):
		var content mcp.Content
		if isEnabled {
			id, url, err := mediaService.SaveContent(ctx, data, contentType)
			if err != nil {
				return nil, err
			}
			content = &mcp.ResourceLink{
				URI:         url,
				Name:        id,
				MIMEType:    contentType,
				Description: "When using the data, please use the accessible URL",
			}
		} else {
			content = &mcp.ImageContent{
				Data:     data,
				MIMEType: contentType,
			}
		}
		return []mcp.Content{content}, nil
	case strings.HasPrefix(baseType, "audio/"):
		var content mcp.Content
		if isEnabled {
			id, url, err := mediaService.SaveContent(ctx, data, contentType)
			if err != nil {
				return nil, err
			}
			content = &mcp.ResourceLink{
				URI:         url,
				Name:        id,
				MIMEType:    contentType,
				Description: "When using the data, please use the accessible URL",
			}
		} else {
			content = &mcp.AudioContent{
				Data:     data,
				MIMEType: contentType,
			}
		}
		return []mcp.Content{content}, nil
	default:
		var content mcp.Content
		if isEnabled {
			id, url, err := mediaService.SaveContent(ctx, data, contentType)
			if err != nil {
				return nil, err
			}
			content = &mcp.ResourceLink{
				URI:         url,
				Name:        id,
				MIMEType:    contentType,
				Description: "When using the data, please use the accessible URL",
			}
		} else {
			content = &mcp.TextContent{
				Text: string(data),
			}
		}
		return []mcp.Content{content}, nil
	}
}
