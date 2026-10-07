package mcpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// A backend tool that merely starts with an attached agent's "<agent>__"
// prefix is still the service's own tool: tools.exclude hides it, while the
// agent's own <agent>__<skill> tools stay exposed (tools/list, tools/call and
// /mcp/list alike).
func TestToolFilter_ServiceAgents_PrefixedBackendToolIsFiltered(t *testing.T) {
	t.Setenv("TEST", "true")
	backendSrv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	for _, name := range []string{"echo", "translator__secret"} {
		backendSrv.AddTool(
			&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: name}},
				}, nil
			},
		)
	}
	backend := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendSrv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(backend.Close)

	translator := newStubA2AAgent(t, false)
	translator.setResult(agentMessage("translated"))
	agents := config.Agents{"translator": serviceAgentConfig(translator)}
	for name, agent := range agents {
		agent.Name = name
	}
	servers := config.Servers{"billing": &config.Server{
		Name:      "billing",
		Transport: config.MCPTransportHTTP,
		URL:       backend.URL,
		Agents:    agents,
		Tools:     &config.ToolsConfig{Exclude: []string{"translator__*"}},
	}}
	s := NewMCPServer(servers, nil)
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)
	session := connectInMemory(t, s, "billing")

	require.Equal(t,
		[]string{"echo", "translator__translate", "translator__summarize"},
		sessionToolNames(t, session),
		"the excluded backend tool is hidden; the agent's tools are not filtered",
	)
	// The name is routed by its agent prefix, so the excluded backend tool is
	// never reached (the agent answers instead).
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "translator__secret",
		Arguments: map[string]any{"sessionId": "s", "message": "hi"},
	})
	if err == nil {
		for _, c := range res.Content {
			if text, ok := c.(*mcp.TextContent); ok {
				require.NotEqual(t, "translator__secret", text.Text,
					"reached the excluded backend tool")
			}
		}
	}
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "translator__translate",
		Arguments: map[string]any{"sessionId": "s", "message": "hi"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	infos, err := s.ToolCatalog(t.Context(), "billing")
	require.NoError(t, err)
	var names []string
	for _, info := range infos {
		names = append(names, info.Name)
	}
	require.Equal(t,
		[]string{"echo", "translator__translate", "translator__summarize"}, names,
		"/mcp/list filters the service's tools the same way and keeps the agent's",
	)
}
