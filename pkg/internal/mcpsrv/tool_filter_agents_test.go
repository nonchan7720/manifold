package mcpsrv

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// agents の <agent>__<skill> ツールは tools.include に含まれなくても見え、
// 呼べる一方、バックエンド自身のツールは従来通り絞られること。
func TestToolFilter_ServiceAgents_NotFiltered(t *testing.T) {
	t.Setenv("TEST", "true")
	backend := newAuthzMCPBackendServer(t)
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
		Tools:     &config.ToolsConfig{Include: []string{"echo"}},
	}}
	s := NewMCPServer(servers, nil)
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)
	session := connectInMemory(t, s, "billing")

	require.Equal(t,
		[]string{"echo", "translator__translate", "translator__summarize"},
		sessionToolNames(t, session),
	)
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "translator__translate",
		Arguments: map[string]any{"sessionId": "s", "message": "hi"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	_, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "secret"})
	require.ErrorContains(t, err, "unknown tool")
}
