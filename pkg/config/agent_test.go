package config

import (
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validAgent() *Agent {
	return &Agent{
		Name:        "translator",
		Description: "Use for translation.",
		URL:         "https://agent.example.com",
	}
}

func TestAgent_Validate_Valid(t *testing.T) {
	require.NoError(t, validAgent().ValidateWithContext(t.Context()))
}

func TestAgent_Validate_DescriptionRequired(t *testing.T) {
	a := validAgent()
	a.Description = ""
	err := a.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Description")
}

func TestAgent_Validate_URLRequiredAndAbsolute(t *testing.T) {
	a := validAgent()
	a.URL = ""
	require.Error(t, a.ValidateWithContext(t.Context()))

	a.URL = "/relative"
	err := a.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "URL")
}

func TestAgent_Validate_SingleAuthOnly(t *testing.T) {
	a := validAgent()
	a.AuthValue = &AuthValue{Header: "Authorization", Value: "x"}
	a.TokenExchange = &TokenExchange{URL: "https://sts.example.com/token"}
	err := a.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "only one of authValue, oauth2, tokenExchange")
}

func TestAgent_Validate_NestedOAuth2(t *testing.T) {
	a := validAgent()
	a.OAuth2 = &OAuth2{
		ClientID: "id", ClientSecret: "secret", AuthURL: "not-a-url", TokenURL: "https://t",
	}
	err := a.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "AuthURL")
}

func TestAgent_Validate_NegativeTimeout(t *testing.T) {
	a := validAgent()
	a.Timeout = -time.Second
	require.Error(t, a.ValidateWithContext(t.Context()))
}

func TestAgent_Server_CarriesSettingsAsA2ATransport(t *testing.T) {
	a := validAgent()
	a.AgentCardPath = "/cards/agent.json"
	a.ExtraHeaders = map[string]string{"X-Tenant": "acme"}
	a.Timeout = 5 * time.Second
	a.AuthValue = &AuthValue{Header: "X-Api-Key", Value: "k"}

	srv := a.Server()
	require.Equal(t, "translator", srv.Name)
	require.Equal(t, "Use for translation.", srv.Description)
	require.Equal(t, MCPTransportA2A, srv.Transport)
	require.True(t, srv.IsA2ABackend())
	require.False(t, srv.IsMCPBackend())
	require.False(t, srv.IsReverseBackend())
	require.False(t, srv.IsOpenAPI())
	require.Equal(t, "https://agent.example.com", srv.URL)
	require.Equal(t, "/cards/agent.json", srv.AgentCardPath)
	require.Equal(t, "/cards/agent.json", srv.AgentCardPathOrDefault())
	require.Equal(t, a.ExtraHeaders, srv.ExtraHeaders)
	require.Equal(t, a.AuthValue, srv.AuthValue)
	require.Equal(t, 5*time.Second, srv.CallTimeoutOrDefault())
}

func TestServer_AgentCardPathOrDefault_Default(t *testing.T) {
	require.Equal(t, DefaultAgentCardPath, Server{}.AgentCardPathOrDefault())
}

func TestServer_Validate_A2ATransportRejectedUnderMCPServers(t *testing.T) {
	s := Server{Description: "x", Transport: MCPTransportA2A, URL: "https://agent.example.com"}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "configured under agents")
}

func TestServer_Validate_AgentCardPathRejectedUnderMCPServers(t *testing.T) {
	s := Server{
		Description:   "x",
		Transport:     MCPTransportHTTP,
		URL:           "https://x",
		AgentCardPath: "/card.json",
	}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "agentCardPath is only supported under agents")
}

func TestConfig_Validate_Agents_Valid(t *testing.T) {
	cfg := newValidConfigWithServers(nil)
	cfg.Agents = Agents{"translator": validAgent()}
	require.NoError(t, cfg.ValidateWithContext(t.Context()))
}

func TestConfig_Validate_Agents_InvalidKey(t *testing.T) {
	cfg := newValidConfigWithServers(nil)
	cfg.Agents = Agents{"bad.name": validAgent()}
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid characters")
}

func TestConfig_Validate_Agents_NameCollidesWithMCPServer(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"translator": {Description: "x", Transport: MCPTransportHTTP, URL: "https://x"},
	})
	cfg.Agents = Agents{"translator": validAgent()}
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "used by both agents and mcpServers")
}

func TestConfig_Validate_Agents_InvalidAgentReported(t *testing.T) {
	cfg := newValidConfigWithServers(nil)
	cfg.Agents = Agents{"translator": {Name: "translator", URL: "https://x"}}
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Description")
}

func TestMergeAgentsIntoServers(t *testing.T) {
	servers := mergeAgentsIntoServers(nil, Agents{"translator": validAgent()})
	require.Len(t, servers, 1)
	require.True(t, servers["translator"].IsA2ABackend())

	require.Nil(t, mergeAgentsIntoServers(nil, nil))
}

func TestLoadInternal_AgentsDirective(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifold-agents-test.yaml"), `
include: [agents.yaml]
gateway:
  encryptKey: ${TEST_AGENTS_ENCRYPT_KEY}
sqlite:
  path: ./tmp/manifold.db
mcpServers:
  notion:
    transport: http
    url: https://mcp.notion.com/mcp
    description: notion
agents:
  translator:
    url: https://translator.example.com
    description: Use for translation.
    timeout: 30s
    headers:
      X-Tenant: acme
`)
	writeFile(t, filepath.Join(dir, "agents.yaml"), `
agents:
  planner:
    url: ${TEST_AGENTS_PLANNER_URL}
    agentCardPath: /cards/planner.json
    description: Use for planning.
    authValue:
      header: X-Api-Key
      value: secret
`)
	t.Setenv("TEST_AGENTS_ENCRYPT_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("TEST_AGENTS_PLANNER_URL", "https://planner.example.com")
	t.Chdir(dir)

	cfg, err := loadInternal(t.Context(), "manifold-agents-test")
	require.NoError(t, err)

	require.Len(t, cfg.Agents, 2)
	require.Equal(t, "translator", cfg.Agents["translator"].Name)
	require.Equal(t, "planner", cfg.Agents["planner"].Name)

	// 両方のエージェントが MCP サーバーと並んで a2a トランスポートとして登録される。
	require.Len(t, cfg.MCPServer, 3)
	translator := cfg.MCPServer["translator"]
	require.True(t, translator.IsA2ABackend())
	require.Equal(t, "https://translator.example.com", translator.URL)
	require.Equal(t, "Use for translation.", translator.Description)
	require.Equal(t, 30*time.Second, translator.CallTimeoutOrDefault())
	// viper は map のキーを小文字化する（mcpServers.<name>.headers と同じ挙動）。
	require.Equal(t, "acme", translator.ExtraHeaders["x-tenant"])

	planner := cfg.MCPServer["planner"]
	require.True(t, planner.IsA2ABackend())
	require.Equal(t, "https://planner.example.com", planner.URL)
	require.Equal(t, "/cards/planner.json", planner.AgentCardPath)
	require.NotNil(t, planner.AuthValue)
	require.Equal(t, "secret", planner.AuthValue.Value)

	require.True(t, cfg.MCPServer["notion"].IsMCPBackend())
}

func TestLoadInternal_AgentsDirective_NameCollisionFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifold-agents-dup.yaml"), `
gateway:
  encryptKey: ${TEST_AGENTS_DUP_ENCRYPT_KEY}
sqlite:
  path: ./tmp/manifold.db
mcpServers:
  shared:
    transport: http
    url: https://mcp.example.com/mcp
    description: mcp
agents:
  shared:
    url: https://agent.example.com
    description: agent
`)
	t.Setenv("TEST_AGENTS_DUP_ENCRYPT_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Chdir(dir)

	_, err := loadInternal(t.Context(), "manifold-agents-dup")
	require.Error(t, err)
	require.Contains(t, err.Error(), "used by both agents and mcpServers")
}
