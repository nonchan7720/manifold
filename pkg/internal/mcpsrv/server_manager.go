package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
)

type MCPServer struct {
	servers config.Servers

	srv            *mcp.Server
	appSrv         map[string]*mcp.Server
	backendClients map[string]*MCPBackendClient
}

func NewMCPServer(servers config.Servers) *MCPServer {
	return &MCPServer{
		servers:        servers,
		srv:            mcp.NewServer(&mcp.Implementation{Name: "MCP Gateway", Version: "v1.0.0"}, &mcp.ServerOptions{}),
		appSrv:         map[string]*mcp.Server{},
		backendClients: map[string]*MCPBackendClient{},
	}
}

func (s *MCPServer) Init() error {
	for name, server := range s.servers {
		srv := mcp.NewServer(&mcp.Implementation{Name: name, Version: "v1.0.0"}, &mcp.ServerOptions{})

		if server.IsMCPBackend() {
			// MCP バックエンドモード: 遅延接続のためクライアントを登録するのみ
			s.backendClients[name] = &MCPBackendClient{
				name: name,
				cfg:  server,
				srv:  srv,
			}
		} else {
			// OpenAPI モード: 既存ロジック
			register, err := RegisterOpenAPI(server.Spec, server.BaseURL)
			if err != nil {
				return err
			}
			tools := register.ListTools()
			for _, tool := range tools {
				srv.AddTool(&tool.tool, func(ctx context.Context, ctr *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					var input map[string]any
					if err := json.Unmarshal(ctr.Params.Arguments, &input); err != nil {
						resp := &mcp.CallToolResult{}
						resp.SetError(err)
						return resp, nil
					}
					var result mcp.CallToolResult
					resp, err := tool.handler(ctx, input)
					if err != nil {
						result.SetError(err)
					} else {
						result.Content = append(result.Content, &mcp.TextContent{Text: resp})
					}
					return &result, nil
				})
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
