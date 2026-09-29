package config

import (
	"context"
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

func TestServer_Validate_SkillsRejectedUnderMCPServers(t *testing.T) {
	s := Server{
		Description: "x",
		Transport:   MCPTransportHTTP,
		URL:         "https://x",
		Skills:      []string{"translate"},
	}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "skills is only supported under agents")
}

func TestAgent_Validate_SkillsRejectEmptyAndDuplicate(t *testing.T) {
	a := validAgent()
	a.Skills = []string{"translate", "summarize"}
	require.NoError(t, a.ValidateWithContext(t.Context()))

	a.Skills = []string{" "}
	err := a.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not be empty")

	a.Skills = []string{"translate", "translate"}
	err = a.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "more than once")
}

func TestAgent_Server_CarriesSkills(t *testing.T) {
	a := validAgent()
	a.Skills = []string{"summarize", "translate"}
	require.Equal(t, []string{"summarize", "translate"}, a.Server().Skills)
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
    skills: [translate]
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
	require.Equal(t, []string{"translate"}, translator.Skills)

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

// --- mcpServers.<name>.agents（サービスにぶら下げる A2A エージェント） ---

func validServerWithAgents(agents Agents) Server {
	return Server{
		Description: "Billing service",
		Transport:   MCPTransportHTTP,
		URL:         "https://billing.example.com/mcp",
		Agents:      agents,
	}
}

func TestServer_Validate_NestedAgents_HTTPValid(t *testing.T) {
	s := validServerWithAgents(Agents{
		"translator": validAgent(),
		"reviewer": {
			Description: "Use for review.",
			URL:         "https://reviewer.example.com",
			Skills:      []string{"review"},
			Timeout:     10 * time.Second,
		},
	})
	require.NoError(t, s.ValidateWithContext(t.Context()))
	require.True(t, s.HasAgents())
	require.False(t, Server{}.HasAgents())
}

func TestServer_Validate_NestedAgents_OpenAPIValid(t *testing.T) {
	s := Server{
		Description: "Billing API",
		Spec:        "https://billing.example.com/openapi.json",
		BaseURL:     "https://billing.example.com",
		Agents:      Agents{"translator": validAgent()},
	}
	require.NoError(t, s.ValidateWithContext(t.Context()))
}

func TestServer_Validate_NestedAgents_RejectedForReverse(t *testing.T) {
	s := Server{
		Description: "Page",
		Transport:   MCPTransportReverse,
		Origin:      "https://app.example.com",
		Identity:    "user",
		Agents:      Agents{"translator": validAgent()},
	}
	ctx := context.WithValue(t.Context(), edgeContextKey{}, EdgeConfig{})
	ctx = context.WithValue(ctx, identitiesContextKey{}, map[string]*IdentityProfile{"user": {}})
	err := s.ValidateWithContext(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agents is not supported for the reverse transport")
}

func TestServer_Validate_NestedAgents_NameMustNotContainSeparator(t *testing.T) {
	s := validServerWithAgents(Agents{"trans__lator": validAgent()})
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), `must not contain "__"`)
}

func TestServer_Validate_NestedAgents_NameCharacters(t *testing.T) {
	s := validServerWithAgents(Agents{"trans.lator": validAgent()})
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid characters")
}

func TestServer_Validate_NestedAgents_OAuth2Rejected(t *testing.T) {
	a := validAgent()
	a.OAuth2 = &OAuth2{
		ClientID:     "id",
		ClientSecret: "secret",
		AuthURL:      "https://auth.example.com/authorize",
		TokenURL:     "https://auth.example.com/token",
	}
	s := validServerWithAgents(Agents{"translator": a})
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "oauth2 is not supported for agents under mcpServers")
}

func TestServer_Validate_NestedAgents_InvalidAgentReportedWithName(t *testing.T) {
	a := validAgent()
	a.Description = ""
	s := validServerWithAgents(Agents{"translator": a})
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), `agent "translator"`)
	require.Contains(t, err.Error(), "Description")
}

func TestAgentToolName(t *testing.T) {
	require.Equal(t, "translator__translate", AgentToolName("translator", "translate"))
	require.Equal(t, "translator__", AgentToolName("translator", ""))
}

func TestLoadInternal_NestedAgents(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifold-nested-agents.yaml"), `
include: [services.yaml]
gateway:
  encryptKey: ${TEST_NESTED_AGENTS_ENCRYPT_KEY}
sqlite:
  path: ./tmp/manifold.db
mcpServers:
  billing:
    transport: http
    url: https://billing.example.com/mcp
    description: Billing service
    agents:
      translator:
        url: https://translator.example.com
        agentCardPath: /cards/translator.json
        description: Use for translation.
        skills: [translate]
        timeout: 30s
      reviewer:
        url: https://reviewer.example.com
        description: Use for review.
`)
	writeFile(t, filepath.Join(dir, "services.yaml"), `
mcpServers:
  search:
    transport: http
    url: https://search.example.com/mcp
    description: Search service
    agents:
      summarizer:
        url: https://summarizer.example.com
        description: Use for summarization.
`)
	t.Setenv("TEST_NESTED_AGENTS_ENCRYPT_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Chdir(dir)

	cfg, err := loadInternal(t.Context(), "manifold-nested-agents")
	require.NoError(t, err)

	billing := cfg.MCPServer["billing"]
	require.True(t, billing.IsMCPBackend())
	require.True(t, billing.HasAgents())
	require.Len(t, billing.Agents, 2)

	translator := billing.Agents["translator"]
	require.Equal(t, "translator", translator.Name)
	require.Equal(t, "https://translator.example.com", translator.URL)
	require.Equal(t, "/cards/translator.json", translator.AgentCardPath)
	require.Equal(t, []string{"translate"}, translator.Skills)
	require.Equal(t, 30*time.Second, translator.Timeout)

	reviewer := billing.Agents["reviewer"]
	require.Equal(t, "reviewer", reviewer.Name)
	require.Empty(t, reviewer.Skills)

	// include 先の mcpServers にぶら下げたエージェントも読み込まれる。
	search := cfg.MCPServer["search"]
	require.Len(t, search.Agents, 1)
	require.Equal(t, "summarizer", search.Agents["summarizer"].Name)
	require.Equal(t, "https://summarizer.example.com", search.Agents["summarizer"].URL)

	// mcpServers 配下のエージェントはトップレベルの agents には現れない。
	require.Empty(t, cfg.Agents)
}
