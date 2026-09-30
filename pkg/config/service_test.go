package config

import (
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServer_ServiceCode_DefaultsToServerName(t *testing.T) {
	s := Server{Name: "billing-api"}
	require.Equal(t, "billing-api", s.ServiceCode())
	require.Equal(t, "billing-api", s.ServiceName())

	// name だけ設定した場合もコードはサーバー名のまま。
	s.Service = &Service{Name: "Billing"}
	require.Equal(t, "billing-api", s.ServiceCode())
	require.Equal(t, "Billing", s.ServiceName())
}

func TestServer_ServiceCode_FromService(t *testing.T) {
	s := Server{Name: "billing-api", Service: &Service{Code: "billing"}}
	require.Equal(t, "billing", s.ServiceCode())
	// name 未設定なら表示名はコード。
	require.Equal(t, "billing", s.ServiceName())

	s.Service.Name = "請求サービス"
	require.Equal(t, "請求サービス", s.ServiceName())
}

func TestServer_Validate_ServiceCodeCharacters(t *testing.T) {
	s := Server{
		Description: "x",
		Transport:   MCPTransportHTTP,
		URL:         "https://x.example.com/mcp",
		Service:     &Service{Code: "billing/api"},
	}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Service")

	s.Service.Code = "billing_api-1"
	require.NoError(t, s.ValidateWithContext(t.Context()))
}

func TestServer_Validate_ServiceNameBlank(t *testing.T) {
	s := Server{
		Description: "x",
		Transport:   MCPTransportHTTP,
		URL:         "https://x.example.com/mcp",
		Service:     &Service{Code: "billing", Name: "   "},
	}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not be blank")
}

func TestAgent_Validate_ServiceCodeCharacters(t *testing.T) {
	a := validAgent()
	a.Service = &Service{Code: "bad.code"}
	err := a.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Service")
}

func TestAgent_Server_CarriesService(t *testing.T) {
	a := validAgent()
	a.Service = &Service{Code: "billing", Name: "Billing"}
	srv := a.Server()
	require.Equal(t, "billing", srv.ServiceCode())
	require.Equal(t, "Billing", srv.ServiceName())
}

func TestServer_Validate_NestedAgents_ServiceRejected(t *testing.T) {
	agent := validAgent()
	agent.Service = &Service{Code: "billing"}
	s := validServerWithAgents(Agents{"translator": agent})
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "service is not supported for agents under mcpServers")
}

func TestConfig_Validate_ServiceNameConflict(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"billing-api": {
			Description: "x", Transport: MCPTransportHTTP, URL: "https://x",
			Service: &Service{Code: "billing", Name: "Billing"},
		},
	})
	agent := validAgent()
	agent.Service = &Service{Code: "billing", Name: "Invoices"}
	cfg.Agents = Agents{"billing-agent": agent}
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), `service "billing" has conflicting names`)
}

func TestConfig_Validate_ServiceNameSharedOrOmitted(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"billing-api": {
			Description: "x", Transport: MCPTransportHTTP, URL: "https://x",
			Service: &Service{Code: "billing", Name: "Billing"},
		},
		"billing-admin": {
			Description: "y", Transport: MCPTransportHTTP, URL: "https://y",
			Service: &Service{Code: "billing"},
		},
	})
	agent := validAgent()
	agent.Service = &Service{Code: "billing", Name: "Billing"}
	cfg.Agents = Agents{"billing-agent": agent}
	require.NoError(t, cfg.ValidateWithContext(t.Context()))
}

func TestLoadInternal_Service(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifold-service-test.yaml"), `
gateway:
  encryptKey: ${TEST_SERVICE_ENCRYPT_KEY}
sqlite:
  path: ./tmp/manifold.db
mcpServers:
  billing-api:
    transport: http
    url: https://billing.example.com/mcp
    description: billing API
    service:
      code: billing
      name: 請求サービス
  billing-admin:
    transport: http
    url: https://billing-admin.example.com/mcp
    description: billing admin API
    service:
      code: billing
  notion:
    transport: http
    url: https://mcp.notion.com/mcp
    description: notion
agents:
  billing-agent:
    url: https://agent.example.com
    description: Use for billing questions.
    service:
      code: billing
`)
	t.Setenv("TEST_SERVICE_ENCRYPT_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Chdir(dir)

	cfg, err := loadInternal(t.Context(), "manifold-service-test")
	require.NoError(t, err)

	for _, name := range []string{"billing-api", "billing-admin", "billing-agent"} {
		srv := cfg.MCPServer[name]
		require.Equal(t, "billing", srv.ServiceCode(), name)
		// 表示名を省略したエントリも、同じサービスの表示名で補われる。
		require.Equal(t, "請求サービス", srv.ServiceName(), name)
	}

	notion := cfg.MCPServer["notion"]
	require.Nil(t, notion.Service)
	require.Equal(t, "notion", notion.ServiceCode())
	require.Equal(t, "notion", notion.ServiceName())
}
