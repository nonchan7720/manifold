package mcpsrv

import (
	"context"
	"encoding/base64"
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

	mediaUploader storage.MediaUploader
}

func NewMCPServer(servers config.Servers, mediaUploader storage.MediaUploader) *MCPServer {
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
			err := registerAPI(ctx, server.Spec, server.BaseURL, server.ExtraHeaders, srv, s.mediaUploader)
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

func registerAPI(ctx context.Context, spec, baseURL string, headers map[string]string, srv *mcp.Server, mediaUploader storage.MediaUploader) error {
	// OpenAPI モード: 既存ロジック
	register, err := RegisterOpenAPI(ctx, spec, baseURL, headers)
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
			} else {
				content, err := generateContent(ctx, contentType, resp, mediaUploader)
				if err != nil {
					result.SetError(err)
				} else {
					result.Content = content
					if json.Valid(resp) {
						result.StructuredContent = json.RawMessage(resp)
					}
				}
			}
			return &result, nil
		})
	}
	return nil
}

func decodeBase64(v []byte) (raw []byte, ok bool) {
	dst := make([]byte, base64.URLEncoding.DecodedLen(len(v)))
	_, err := base64.URLEncoding.Decode(dst, v)
	if err == nil {
		return dst, true
	}
	return nil, false
}

func generateContent(ctx context.Context, contentType string, data []byte, mediaUploader storage.MediaUploader) ([]mcp.Content, error) {
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
	isEnabled := mediaUploader.Enabled()
	if isEnabled {
		rawData, base64Ok := decodeBase64(data)
		if base64Ok {
			data = rawData
		}
	}
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
			id, url, err := mediaUploader.Do(ctx, data, contentType)
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
			id, url, err := mediaUploader.Do(ctx, data, contentType)
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
			id, url, err := mediaUploader.Do(ctx, data, contentType)
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
