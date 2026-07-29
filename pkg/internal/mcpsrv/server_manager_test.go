package mcpsrv

import (
	"context"
	"encoding/base64"
	"io"
	"net/url"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/stretchr/testify/require"
)

// petstoreOperationCount は fixtures/petstore_oas.json 内の operationId 数（手動でカウント済み）。
const petstoreOperationCount = 19

// fakeMediaService は Enabled() を true にした状態での Do() 呼び出し内容を検証するためのテスト用スタブ。
type fakeMediaService struct {
	enabled bool
	doFunc  func(ctx context.Context, data []byte, contentType string) (string, string, error)
}

func (f *fakeMediaService) SaveContent(ctx context.Context, data []byte, contentType string) (string, string, error) {
	return f.doFunc(ctx, data, contentType)
}

func (f *fakeMediaService) AccessCheck(ctx context.Context) error { return nil }

func (f *fakeMediaService) Enabled() bool { return f.enabled }

func (f *fakeMediaService) DownloadContent(ctx context.Context, id string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

func TestGenerateContent_BinaryImage_NoopUploader(t *testing.T) {
	raw := []byte("raw-image-bytes")
	encoded := []byte(base64.URLEncoding.EncodeToString(raw))

	// mediaUploader が無効の場合、デコードはバックエンドサービス側の責務ではないため
	// base64 のまま ImageContent.Data に格納される。
	contents, err := generateContent(context.Background(), "image/png", encoded, storage.NewNoopUploader())
	require.NoError(t, err)
	require.Len(t, contents, 1)

	img, ok := contents[0].(*mcp.ImageContent)
	require.True(t, ok)
	require.Equal(t, encoded, img.Data)
}

func TestGenerateContent_BinaryImage_EnabledUploader(t *testing.T) {
	raw := []byte("raw-image-bytes")
	encoded := []byte(base64.URLEncoding.EncodeToString(raw))

	var gotData []byte
	mediaService := &fakeMediaService{
		enabled: true,
		doFunc: func(ctx context.Context, data []byte, contentType string) (string, string, error) {
			gotData = data
			return "media-id", "https://example.com/media-id", nil
		},
	}

	// generateContent 自体はデコードを行わず、そのまま MediaUploadService.Do に渡す。
	// デコード（と失敗時のフォールバック）は storage.MediaUpload.Do が担う。
	contents, err := generateContent(context.Background(), "image/png", encoded, mediaService)
	require.NoError(t, err)
	require.Len(t, contents, 1)

	link, ok := contents[0].(*mcp.ResourceLink)
	require.True(t, ok)
	require.Equal(t, "media-id", link.Name)
	require.Equal(t, encoded, gotData)
}

func TestNewMCPServer(t *testing.T) {
	servers := config.Servers{
		"test": &config.Server{Spec: "fixtures/petstore_oas.json"},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NotNil(t, s)
	require.NotNil(t, s.mediaUploader)
	require.NotNil(t, s.srv)
	require.NotNil(t, s.appSrv)
	require.NotNil(t, s.backendClients)
}

func TestMCPServer_Init_OpenAPIMode(t *testing.T) {
	servers := config.Servers{
		"petstore": &config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	srv, err := s.Server("petstore")
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestMCPServer_Init_SwaggerMode(t *testing.T) {
	servers := config.Servers{
		"swagger": &config.Server{
			Spec:    "fixtures/petstore_swagger.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	srv, err := s.Server("swagger")
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestMCPServer_Init_MCPBackendMode(t *testing.T) {
	servers := config.Servers{
		"backend": &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	// MCP バックエンドモードのサーバーも appSrv に登録される
	srv, err := s.Server("backend")
	require.NoError(t, err)
	require.NotNil(t, srv)

	// バックエンドクライアントも登録される
	bc, ok := s.BackendClient("backend")
	require.True(t, ok)
	require.NotNil(t, bc)
}

func TestMCPServer_Init_InvalidSpec(t *testing.T) {
	servers := config.Servers{
		"invalid": &config.Server{
			Spec: "fixtures/nonexistent.json",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.Error(t, err)
}

func TestMCPServer_Server_NotFound(t *testing.T) {
	servers := config.Servers{}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	_ = s.Init(context.Background())

	_, err := s.Server("nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found mcp server")
}

func TestMCPServer_BackendClient_NotFound(t *testing.T) {
	servers := config.Servers{}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	_ = s.Init(context.Background())

	bc, ok := s.BackendClient("nonexistent")
	require.False(t, ok)
	require.Nil(t, bc)
}

func TestMCPServer_Close_NoBackends(t *testing.T) {
	servers := config.Servers{
		"openapi": &config.Server{Spec: "fixtures/petstore_oas.json"},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	// バックエンドがない場合も Close はパニックしない
	require.NotPanics(t, func() {
		s.Close()
	})
}

func TestMCPServer_Close_WithBackend(t *testing.T) {
	servers := config.Servers{
		"backend": &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	// 接続していないバックエンドを Close してもパニックしない
	require.NotPanics(t, func() {
		s.Close()
	})
}

// --- tool_search: catalog 連携 ---

func TestMCPServer_Init_OpenAPIMode_PopulatesCatalog(t *testing.T) {
	servers := config.Servers{
		"petstore": &config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	require.NotNil(t, s.catalog)
	require.Equal(t, petstoreOperationCount, s.catalog.Total())
}

func TestMCPServer_Init_DefaultToolSearchThreshold_RealToolsVisible(t *testing.T) {
	servers := config.Servers{
		"petstore": &config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	// デフォルト閾値（100）はフィクスチャのツール数（19）を上回るため実ツールがそのまま見える
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	srv, err := s.Server("petstore")
	require.NoError(t, err)

	session := connectInMemory(t, context.Background(), srv)
	result, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, result.Tools, petstoreOperationCount)
	require.NotContains(t, toolNames(result.Tools), toolSearchName)
}

func TestMCPServer_Init_LowToolSearchThreshold_OnlyToolSearchVisible(t *testing.T) {
	servers := config.Servers{
		"petstore": &config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(
		servers,
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
		WithToolSearchConfig(config.ToolSearchConfig{Threshold: 1, DefaultLimit: 10}),
	)
	err := s.Init(context.Background())
	require.NoError(t, err)

	srv, err := s.Server("petstore")
	require.NoError(t, err)

	session := connectInMemory(t, context.Background(), srv)
	result, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, result.Tools, 1)
	require.Equal(t, toolSearchName, result.Tools[0].Name)
}

func TestMCPServer_Init_MultipleServers(t *testing.T) {
	servers := config.Servers{
		"oas": &config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore1.example.com",
		},
		"swagger": &config.Server{
			Spec:    "fixtures/petstore_swagger.json",
			BaseURL: "https://petstore2.example.com",
		},
		"mcp": &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://mcp.example.com/mcp",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	// 全サーバーが登録されている
	for name := range servers {
		srv, err := s.Server(name)
		require.NoError(t, err, "server %s should be registered", name)
		require.NotNil(t, srv)
	}

	// MCP バックエンドのみ BackendClient がある
	bc, ok := s.BackendClient("mcp")
	require.True(t, ok)
	require.NotNil(t, bc)

	_, ok = s.BackendClient("oas")
	require.False(t, ok)
}
