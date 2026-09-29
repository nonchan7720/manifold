package mcpsrv

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/require"
)

// serviceAgentConfig は stub を向く mcpServers.<name>.agents 配下のエージェント設定を返す。
func serviceAgentConfig(stub *stubA2AAgent, skills ...string) *config.Agent {
	return &config.Agent{
		Description: "Use this agent for the billing workflow.",
		URL:         stub.srv.URL,
		Skills:      skills,
	}
}

// connectInMemory は MCPServer の name サーバーへインメモリ接続したセッションを返す。
func connectInMemory(t *testing.T, s *MCPServer, name string) *mcp.ClientSession {
	t.Helper()
	srv, err := s.Server(name)
	require.NoError(t, err)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err = srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// newMCPServiceWithAgents は http MCP バックエンドの "billing" サービスに agents を
// ぶら下げた MCPServer を Init 済みで返す。
func newMCPServiceWithAgents(
	t *testing.T, backendURL string, agents config.Agents, opts ...Option,
) *MCPServer {
	t.Helper()
	for name, agent := range agents {
		agent.Name = name
	}
	servers := config.Servers{"billing": &config.Server{
		Name:        "billing",
		Description: "Billing service",
		Transport:   config.MCPTransportHTTP,
		URL:         backendURL,
		Agents:      agents,
	}}
	s := NewMCPServer(servers, nil, opts...)
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)
	return s
}

func TestServiceAgents_MCPBackend_ListAndCall(t *testing.T) {
	t.Setenv("TEST", "true")
	backend := newAuthzMCPBackendServer(t)
	translator := newStubA2AAgent(t, false)
	translator.setResult(agentMessage("translated"))
	reviewer := newStubA2AAgent(t, false)
	reviewer.setResult(agentMessage("reviewed"))

	s := newMCPServiceWithAgents(t, backend.URL, config.Agents{
		"translator": serviceAgentConfig(translator),
		"reviewer":   serviceAgentConfig(reviewer, stubSkillSummarize),
	})
	session := connectInMemory(t, s, "billing")

	caps := session.InitializeResult().Capabilities
	require.NotNil(t, caps.Tools)

	// サービス自身のツール、続けてエージェント名順（reviewer, translator）に
	// <agent>__<skill> のツールが並ぶ。
	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{
		"echo", "secret",
		"reviewer__summarize",
		"translator__translate", "translator__summarize",
	}, toolNames(listed.Tools))
	require.Equal(t, "public", listed.CacheScope)

	// エージェントのツールは呼び出し元の sessionId を contextId にして届く。
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "translator__translate",
		Arguments: map[string]any{"sessionId": "sess-42", "message": "hello"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	req := translator.lastRequest(t)
	require.Equal(t, "sess-42", req.Message.ContextID)
	require.Equal(t, stubSkillTranslate, req.Message.Metadata[a2aSkillMetadataKey])
	reviewer.mu.Lock()
	require.Empty(t, reviewer.requests, "only the routed agent receives the call")
	reviewer.mu.Unlock()

	// サービス自身のツールは引き続きバックエンドへ届く。
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "echo"})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, "echoed", resultText(t, res))

	// skills で絞った reviewer の translate は公開されていない。
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "reviewer__translate",
		Arguments: map[string]any{"sessionId": "s", "message": "hi"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, resultText(t, res), "unknown skill")

	// 存在するエージェントの存在しないスキル。
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "translator__nope",
		Arguments: map[string]any{"sessionId": "s", "message": "hi"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, resultText(t, res), `unknown skill "translator__nope"`)

	// 存在しないエージェント宛はサービス（バックエンド）側の unknown tool になる。
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "nope__x"})
	if err == nil {
		require.True(t, res.IsError)
	} else {
		require.Contains(t, err.Error(), "unknown tool")
	}
}

func TestServiceAgents_OpenAPIService_ListAndCatalog(t *testing.T) {
	t.Setenv("TEST", "true")
	translator := newStubA2AAgent(t, false)
	translator.setResult(agentMessage("translated"))

	agent := serviceAgentConfig(translator)
	agent.Name = "translator"
	servers := config.Servers{"petstore": &config.Server{
		Name:        "petstore",
		Description: "Pet store",
		Spec:        "fixtures/petstore_oas.json",
		BaseURL:     "https://petstore.example.com",
		Agents:      config.Agents{"translator": agent},
	}}
	s := NewMCPServer(servers, nil)
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)
	session := connectInMemory(t, s, "petstore")

	// OpenAPI モードは spec リフレッシュで tools/list_changed を送るため、
	// エージェントをぶら下げても listChanged: true の広告を保つ。
	caps := session.InitializeResult().Capabilities
	require.NotNil(t, caps.Tools)
	require.True(t, caps.Tools.ListChanged)

	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	names := toolNames(listed.Tools)
	require.Contains(t, names, "getpetbyid")
	// OpenAPI のツールが先、エージェントのツールが最後。
	require.Equal(
		t,
		[]string{"translator__translate", "translator__summarize"},
		names[len(names)-2:],
	)
	require.Equal(t, "public", listed.CacheScope)

	catalog, err := s.ToolCatalog(t.Context(), "petstore")
	require.NoError(t, err)
	var catalogNames []string
	for _, info := range catalog {
		catalogNames = append(catalogNames, info.Name)
	}
	require.Contains(t, catalogNames, "getpetbyid")
	require.Equal(t,
		[]string{"translator__translate", "translator__summarize"},
		catalogNames[len(catalogNames)-2:])
	require.Len(t, catalogNames, len(names))

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "translator__summarize",
		Arguments: map[string]any{"sessionId": "sess-1", "message": "hi"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(
		t,
		stubSkillSummarize,
		translator.lastRequest(t).Message.Metadata[a2aSkillMetadataKey],
	)
}

func TestServiceAgents_UnavailableAgentIsSkipped(t *testing.T) {
	t.Setenv("TEST", "true")
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(
		slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})),
	)
	t.Cleanup(func() { slog.SetDefault(prev) })

	backend := newAuthzMCPBackendServer(t)
	healthy := newStubA2AAgent(t, false)
	down := newStubA2AAgent(t, false)
	downCfg := serviceAgentConfig(down)
	down.srv.Close() // Agent Card を取得できない

	s := newMCPServiceWithAgents(t, backend.URL, config.Agents{
		"healthy": serviceAgentConfig(healthy),
		"down":    downCfg,
	})
	session := connectInMemory(t, s, "billing")

	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{
		"echo", "secret",
		"healthy__translate", "healthy__summarize",
	}, toolNames(listed.Tools))
	require.Contains(t, logs.String(), "agent=down")
	require.Contains(t, logs.String(), "server=billing")

	// /mcp/list 用のカタログも同じ方針。
	catalog, err := s.ToolCatalog(t.Context(), "billing")
	require.NoError(t, err)
	var names []string
	for _, info := range catalog {
		names = append(names, info.Name)
	}
	require.Equal(t, []string{
		"echo", "secret",
		"healthy__translate", "healthy__summarize",
	}, names)
}

// denyToolDecider は deny に挙げたツール名だけを拒否する Decider。
type denyToolDecider struct {
	deny  string
	calls []authz.ToolRef
}

func (d *denyToolDecider) Allow(
	_ context.Context,
	_ authz.Principal,
	t authz.ToolRef,
) (bool, error) {
	d.calls = append(d.calls, t)
	return t.Name != d.deny, nil
}

func (d *denyToolDecider) AllowedTools(
	_ context.Context, _ authz.Principal, tools []authz.ToolRef,
) ([]authz.ToolRef, error) {
	var allowed []authz.ToolRef
	for _, tool := range tools {
		if tool.Name != d.deny {
			allowed = append(allowed, tool)
		}
	}
	return allowed, nil
}

func (d *denyToolDecider) AllowCatalog(context.Context, authz.Principal) (bool, error) {
	return true, nil
}

func TestServiceAgents_AuthzPerAgentSkill(t *testing.T) {
	t.Setenv("TEST", "true")
	backend := newAuthzMCPBackendServer(t)
	translator := newStubA2AAgent(t, false)
	translator.setResult(agentMessage("ok"))

	d := &denyToolDecider{deny: "translator__translate"}
	s := newMCPServiceWithAgents(t, backend.URL,
		config.Agents{"translator": serviceAgentConfig(translator)},
		WithServerMiddleware(func(name string) []mcp.Middleware {
			return []mcp.Middleware{NewAuthzMiddleware(name, d, testAuthzHeaders(), nil)}
		}),
	)
	srv, err := s.Server("billing")
	require.NoError(t, err)
	httpSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(httpSrv.Close)
	headers := http.Header{}
	headers.Set("x-user-id", "alice")
	headers.Set("x-user-groups", "readers")
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   httpSrv.URL,
		HTTPClient: &http.Client{Transport: &headerRoundTripper{headers: headers}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	// tools/list は authz が外側でフィルタするため、拒否したスキルだけが消える。
	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"echo", "secret", "translator__summarize"}, toolNames(listed.Tools))

	// 拒否されたスキルの呼び出しはエージェントに届かない。
	_, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "translator__translate",
		Arguments: map[string]any{"sessionId": "s", "message": "hi"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Equal(t, authz.ToolRef{Server: "billing", Name: "translator__translate"},
		d.calls[len(d.calls)-1])
	translator.mu.Lock()
	require.Empty(t, translator.requests)
	translator.mu.Unlock()

	// 許可されたスキルは届く。
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "translator__summarize",
		Arguments: map[string]any{"sessionId": "sess-9", "message": "hi"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, "sess-9", translator.lastRequest(t).Message.ContextID)
}

func TestA2ABackendClient_ToolPrefix(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	stub.setResult(agentMessage("ok"))
	c := NewA2ABackendClient("billing/translator", stubAgentServer(stub), nil,
		withToolPrefix("translator__"))
	t.Cleanup(c.Close)

	res, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t,
		[]string{"translator__translate", "translator__summarize"}, toolNames(res.Tools))
	// Title はスキル名のまま。
	require.Equal(t, "Translate", res.Tools[0].Title)

	infos, err := c.ListToolInfos(t.Context())
	require.NoError(t, err)
	require.Equal(t, "translator__translate", infos[0].Name)

	call, err := c.CallTool(t.Context(), "translator__translate",
		callArgs(t, map[string]any{"sessionId": "s", "message": "hi"}))
	require.NoError(t, err)
	require.False(t, call.IsError)
	require.Equal(t, stubSkillTranslate, stub.lastRequest(t).Message.Metadata[a2aSkillMetadataKey])

	// 接頭辞の無い名前は未知のスキル。
	call, err = c.CallTool(t.Context(), stubSkillTranslate,
		callArgs(t, map[string]any{"sessionId": "s", "message": "hi"}))
	require.NoError(t, err)
	require.True(t, call.IsError)
	require.Contains(t, resultText(t, call), "unknown skill")
}
