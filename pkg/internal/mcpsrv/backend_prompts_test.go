package mcpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// newPromptsBackendServer は withPrompts なら prompt を1つ持つ MCP バックエンドを立てる。
func newPromptsBackendServer(t *testing.T, withPrompts bool) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{Name: "noop", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		},
	)
	if withPrompts {
		srv.AddPrompt(
			&mcp.Prompt{
				Name:        "greet",
				Description: "greet someone",
				Arguments:   []*mcp.PromptArgument{{Name: "name", Required: true}},
			},
			func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{
					Description: "greeting",
					Messages: []*mcp.PromptMessage{{
						Role:    "user",
						Content: &mcp.TextContent{Text: "Hello, " + req.Params.Arguments["name"]},
					}},
				}, nil
			},
		)
	}
	httpSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(httpSrv.Close)
	return httpSrv
}

// MCP バックエンドの prompts を prompts/list・prompts/get で転送する。
func TestBackendPrompts_HTTPBackend_ForwardsPrompts(t *testing.T) {
	for _, tc := range []struct {
		name            string
		protocolVersion string
	}{
		{name: "latest protocol", protocolVersion: ""},
		{name: "legacy protocol", protocolVersion: legacyHostProtocolVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
			httpSrv := newPromptsBackendServer(t, true)
			cs := connectGateway(t, httpSrv.URL, false, false, tc.protocolVersion)
			ctx := context.Background()

			require.NotNil(t, cs.InitializeResult().Capabilities.Prompts)

			list, err := cs.ListPrompts(ctx, nil)
			require.NoError(t, err)
			require.Len(t, list.Prompts, 1)
			require.Equal(t, "greet", list.Prompts[0].Name)
			require.Equal(t, "greet someone", list.Prompts[0].Description)
			require.Len(t, list.Prompts[0].Arguments, 1)

			got, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
				Name:      "greet",
				Arguments: map[string]string{"name": "manifold"},
			})
			require.NoError(t, err)
			require.Equal(t, "greeting", got.Description)
			require.Len(t, got.Messages, 1)
			text, ok := got.Messages[0].Content.(*mcp.TextContent)
			require.True(t, ok)
			require.Equal(t, "Hello, manifold", text.Text)

			_, err = cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "missing"})
			require.Error(t, err)
			require.Contains(t, err.Error(), "unknown prompt")
		})
	}
}

// prompts を持たないバックエンドでは、prompts/list は空、prompts/get は
// unknown prompt を返す（method not found にしない）。
func TestBackendPrompts_HTTPBackend_WithoutPrompts(t *testing.T) {
	t.Setenv("TEST", "true")
	httpSrv := newPromptsBackendServer(t, false)
	cs := connectGateway(t, httpSrv.URL, false, false, "")
	ctx := context.Background()

	list, err := cs.ListPrompts(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, list.Prompts)

	_, err = cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "greet"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown prompt "greet"`)
}
