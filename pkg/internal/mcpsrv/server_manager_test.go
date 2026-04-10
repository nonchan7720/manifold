package mcpsrv

import (
	"context"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMCPServer(t *testing.T) {
	servers := config.Servers{
		"test": config.Server{Spec: "fixtures/petstore_oas.json"},
	}
	s := NewMCPServer(servers)
	require.NotNil(t, s)
	assert.NotNil(t, s.srv)
	assert.NotNil(t, s.appSrv)
	assert.NotNil(t, s.backendClients)
}

func TestMCPServer_Init_OpenAPIMode(t *testing.T) {
	servers := config.Servers{
		"petstore": config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	s := NewMCPServer(servers)
	err := s.Init(context.Background())
	require.NoError(t, err)

	srv, err := s.Server("petstore")
	require.NoError(t, err)
	assert.NotNil(t, srv)
}

func TestMCPServer_Init_SwaggerMode(t *testing.T) {
	servers := config.Servers{
		"swagger": config.Server{
			Spec:    "fixtures/petstore_swagger.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	s := NewMCPServer(servers)
	err := s.Init(context.Background())
	require.NoError(t, err)

	srv, err := s.Server("swagger")
	require.NoError(t, err)
	assert.NotNil(t, srv)
}

func TestMCPServer_Init_MCPBackendMode(t *testing.T) {
	servers := config.Servers{
		"backend": config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
		},
	}
	s := NewMCPServer(servers)
	err := s.Init(context.Background())
	require.NoError(t, err)

	// MCP バックエンドモードのサーバーも appSrv に登録される
	srv, err := s.Server("backend")
	require.NoError(t, err)
	assert.NotNil(t, srv)

	// バックエンドクライアントも登録される
	bc, ok := s.BackendClient("backend")
	assert.True(t, ok)
	assert.NotNil(t, bc)
}

func TestMCPServer_Init_InvalidSpec(t *testing.T) {
	servers := config.Servers{
		"invalid": config.Server{
			Spec: "fixtures/nonexistent.json",
		},
	}
	s := NewMCPServer(servers)
	err := s.Init(context.Background())
	assert.Error(t, err)
}

func TestMCPServer_Server_NotFound(t *testing.T) {
	servers := config.Servers{}
	s := NewMCPServer(servers)
	_ = s.Init(context.Background())

	_, err := s.Server("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found mcp server")
}

func TestMCPServer_BackendClient_NotFound(t *testing.T) {
	servers := config.Servers{}
	s := NewMCPServer(servers)
	_ = s.Init(context.Background())

	bc, ok := s.BackendClient("nonexistent")
	assert.False(t, ok)
	assert.Nil(t, bc)
}

func TestMCPServer_Close_NoBackends(t *testing.T) {
	servers := config.Servers{
		"openapi": config.Server{Spec: "fixtures/petstore_oas.json"},
	}
	s := NewMCPServer(servers)
	err := s.Init(context.Background())
	require.NoError(t, err)

	// バックエンドがない場合も Close はパニックしない
	assert.NotPanics(t, func() {
		s.Close()
	})
}

func TestMCPServer_Close_WithBackend(t *testing.T) {
	servers := config.Servers{
		"backend": config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
		},
	}
	s := NewMCPServer(servers)
	err := s.Init(context.Background())
	require.NoError(t, err)

	// 接続していないバックエンドを Close してもパニックしない
	assert.NotPanics(t, func() {
		s.Close()
	})
}

func TestMCPServer_Init_MultipleServers(t *testing.T) {
	servers := config.Servers{
		"oas": config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore1.example.com",
		},
		"swagger": config.Server{
			Spec:    "fixtures/petstore_swagger.json",
			BaseURL: "https://petstore2.example.com",
		},
		"mcp": config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://mcp.example.com/mcp",
		},
	}
	s := NewMCPServer(servers)
	err := s.Init(context.Background())
	require.NoError(t, err)

	// 全サーバーが登録されている
	for name := range servers {
		srv, err := s.Server(name)
		require.NoError(t, err, "server %s should be registered", name)
		assert.NotNil(t, srv)
	}

	// MCP バックエンドのみ BackendClient がある
	bc, ok := s.BackendClient("mcp")
	assert.True(t, ok)
	assert.NotNil(t, bc)

	_, ok = s.BackendClient("oas")
	assert.False(t, ok)
}
