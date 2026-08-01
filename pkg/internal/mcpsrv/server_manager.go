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
	"github.com/nonchan7720/manifold/pkg/version"
	"go.opentelemetry.io/otel/attribute"
)

type MCPServer struct {
	servers config.Servers

	srv            *mcp.Server
	appSrv         map[string]*mcp.Server
	backendClients map[string]*MCPBackendClient

	mediaUploader *storage.ContentManagementService
}

func NewMCPServer(servers config.Servers, mediaUploader *storage.ContentManagementService) *MCPServer {
	return &MCPServer{
		servers:        servers,
		srv:            mcp.NewServer(&mcp.Implementation{Name: "manifold", Version: version.MarkVersion}, &mcp.ServerOptions{}),
		appSrv:         map[string]*mcp.Server{},
		backendClients: map[string]*MCPBackendClient{},
		mediaUploader:  mediaUploader,
	}
}

func (s *MCPServer) Init(ctx context.Context) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPServer/Init")
	defer func() { trace.EndSpan(ctx, rErr) }()

	for name, server := range s.servers {
		srv := mcp.NewServer(&mcp.Implementation{Name: name, Version: version.MarkVersion}, &mcp.ServerOptions{})

		if server.IsMCPBackend() {
			// MCP バックエンドモード: 遅延接続のためクライアントを登録するのみ
			s.backendClients[name] = &MCPBackendClient{
				name: name,
				cfg:  server,
				srv:  srv,
			}
		} else {
			// OpenAPI モード
			opts := []RegisterOpenAPIOption{
				WithAuth(server.AuthValue),
				WithOAuth2(server.OAuth2),
				WithTokenExchange(server.TokenExchange),
			}
			err := registerAPI(ctx, server.Spec, server.BaseURL, server.ExtraHeaders, srv, s.mediaUploader, opts...)
			if err != nil {
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
	opts ...RegisterOpenAPIOption,
) error {
	// OpenAPI モード: 既存ロジック
	register, err := RegisterOpenAPI(ctx, spec, baseURL, headers, opts...)
	if err != nil {
		return err
	}
	tools := register.ListTools()
	for _, tool := range tools {
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

// resourceLinkDescription は resource_link の説明文を組み立てる。
//
// mimeType フィールドは、受け手（Claude Code 等）が resource_link を
// `[Resource link: {name}] {uri} ({description})` というテキストへ変換する過程で失われる。
// 説明文は変換後も残るため、Content-Type をここにも書いておく。
func resourceLinkDescription(contentType string) string {
	return fmt.Sprintf("Content-Type: %s. When using the data, please use the accessible URL", contentType)
}

// newResourceLink は実体をアップロードし、その参照を表す resource_link を返す。
func newResourceLink(ctx context.Context, data []byte, contentType string, mediaService storage.MediaService) (mcp.Content, error) {
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

func generateContent(ctx context.Context, contentType string, data []byte, mediaService storage.MediaService) ([]mcp.Content, error) {
	// 上流が octet-stream しか返さない場合に備え、実体から型を判定し直す。
	// 判定できた型は振り分け（image/audio/その他）と resource_link の両方に使う。
	contentType = storage.ResolveContentType(contentType, data)
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
