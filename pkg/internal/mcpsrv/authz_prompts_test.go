package mcpsrv

import (
	"context"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/require"
)

// newBillingServerWithPrompts は newBillingServer に prompt を2つ足す。
func newBillingServerWithPrompts(t *testing.T) *mcp.Server {
	t.Helper()
	srv := newBillingServer(t)
	for _, name := range []string{"invoice_howto", "refund_howto"} {
		srv.AddPrompt(
			&mcp.Prompt{Name: name},
			func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{
					Role: "user", Content: &mcp.TextContent{Text: name},
				}}}, nil
			},
		)
	}
	return srv
}

// prompts/get は tools/call と同じ Allow の判定に prompt 名をツール名として渡す。
func TestAuthzMiddleware_PromptGet_UsesToolCallDecision(t *testing.T) {
	d := &fakeDecider{allowResult: true}
	session := newAuthzTestServer(t, newBillingServerWithPrompts(t), d, identityHeaders())

	res, err := session.GetPrompt(t.Context(), &mcp.GetPromptParams{Name: "invoice_howto"})
	require.NoError(t, err)
	require.Len(t, res.Messages, 1)

	require.Equal(t, 1, d.allowCallCount())
	require.Equal(t, "user-042", d.allowCalls[0].p.UserID)
	require.Equal(
		t,
		authz.ToolRef{Server: "billing-svc", Service: "billing", Name: "prompt_invoice_howto"},
		d.allowCalls[0].t,
	)
}

func TestAuthzMiddleware_PromptGet_Denied(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    *fakeDecider
	}{
		{name: "deny", d: &fakeDecider{allowResult: false}},
		{name: "decider error", d: &fakeDecider{allowErr: context.DeadlineExceeded}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newAuthzTestServer(
				t, newBillingServerWithPrompts(t), tc.d, identityHeaders(),
			)
			_, err := session.GetPrompt(t.Context(), &mcp.GetPromptParams{Name: "refund_howto"})
			require.Error(t, err)
			require.Contains(t, err.Error(), "tool not allowed by policy")
			require.NotContains(t, err.Error(), "deadline exceeded")
		})
	}
}

// prompts/list は tools/list と同じ AllowedTools の判定に prompt をツールとして
// 渡し、許可されたものだけを返す。
func TestAuthzMiddleware_PromptsList_UsesToolsListDecision(t *testing.T) {
	d := &fakeDecider{allowedToolsResult: []authz.ToolRef{
		{Server: "billing-svc", Service: "billing", Name: "prompt_refund_howto"},
	}}
	session := newAuthzTestServer(t, newBillingServerWithPrompts(t), d, identityHeaders())

	res, err := session.ListPrompts(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, res.Prompts, 1)
	require.Equal(t, "refund_howto", res.Prompts[0].Name)

	require.Equal(t, 1, d.allowedToolsCallCount())
	require.ElementsMatch(t, []authz.ToolRef{
		{Server: "billing-svc", Service: "billing", Name: "prompt_invoice_howto"},
		{Server: "billing-svc", Service: "billing", Name: "prompt_refund_howto"},
	}, d.allowedToolsCalls[0])
}

func TestAuthzMiddleware_PromptsList_DeciderError(t *testing.T) {
	d := &fakeDecider{allowedToolsErr: context.DeadlineExceeded}
	session := newAuthzTestServer(t, newBillingServerWithPrompts(t), d, identityHeaders())

	_, err := session.ListPrompts(t.Context(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
}

// 呼び出し元を特定できなければ Decider を呼ばずに拒否する。
func TestAuthzMiddleware_Prompts_MissingIdentity(t *testing.T) {
	d := &fakeDecider{allowResult: true}
	session := newAuthzTestServer(t, newBillingServerWithPrompts(t), d, http.Header{})

	_, err := session.ListPrompts(t.Context(), nil)
	require.Error(t, err)
	_, err = session.GetPrompt(t.Context(), &mcp.GetPromptParams{Name: "invoice_howto"})
	require.Error(t, err)
	require.Equal(t, 0, d.allowCallCount())
	require.Equal(t, 0, d.allowedToolsCallCount())
}
