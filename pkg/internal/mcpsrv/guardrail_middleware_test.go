package mcpsrv

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/services/guardrail"
	"github.com/stretchr/testify/require"
)

// fakeGuardrail は呼び出しを記録し、入力の種類（request / response）ごとに
// fn の結果を返す Guardrail。
type fakeGuardrail struct {
	mu       sync.Mutex
	requests []guardrail.Request
	fn       func(req guardrail.Request) (guardrail.Result, error)
}

func (g *fakeGuardrail) Apply(_ context.Context, req guardrail.Request) (guardrail.Result, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req)
	g.mu.Unlock()
	return g.fn(req)
}

// maskEmail は "@" を含むテキストのメールアドレスを [EMAIL] に書き換える。
func maskEmail(req guardrail.Request) (guardrail.Result, error) {
	out := make([]string, len(req.Texts))
	changed := false
	for i, text := range req.Texts {
		out[i] = strings.ReplaceAll(text, "a@example.com", "[EMAIL]")
		changed = changed || out[i] != text
	}
	if !changed {
		return guardrail.Result{Action: guardrail.ActionNone}, nil
	}
	return guardrail.Result{Action: guardrail.ActionIntervened, Texts: out}, nil
}

func guardrailTestClient(
	t *testing.T, g guardrail.Guardrail, preCall, postCall bool, calls *atomic.Int32,
) *mcp.ClientSession {
	t.Helper()
	srv := newToolTestServer(t, calls, "getpet")
	srv.AddReceivingMiddleware(NewGuardrailMiddleware("petstore", "pets", g, preCall, postCall))
	return connectTestClient(t, t.Context(), srv)
}

// pre_call: 書き換えた引数でバックエンドを呼ぶ。
func TestGuardrailMiddleware_PreCallRewritesArguments(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := guardrailTestClient(t, g, true, false, nil)

	got := callText(t, cs, "getpet", map[string]any{"email": "a@example.com"})
	require.Equal(t, `getpet:{"email":"[EMAIL]"}`, got)

	require.Len(t, g.requests, 1)
	require.Equal(t, guardrail.InputTypeRequest, g.requests[0].InputType)
	require.JSONEq(t, `{"email":"a@example.com"}`, g.requests[0].Texts[0])
	require.Equal(t, map[string]any{"server": "petstore", "service": "pets", "tool": "getpet"},
		g.requests[0].Params)
}

// post_call: 結果のテキストを書き換えて返す。
func TestGuardrailMiddleware_PostCallRewritesResult(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := guardrailTestClient(t, g, false, true, nil)

	got := callText(t, cs, "getpet", map[string]any{"email": "a@example.com"})
	require.Equal(t, `getpet:{"email":"[EMAIL]"}`, got)
	require.Len(t, g.requests, 1)
	require.Equal(t, guardrail.InputTypeResponse, g.requests[0].InputType)
}

// 変更が無ければそのまま通す。両方有効なら前後で1回ずつ呼ぶ。
func TestGuardrailMiddleware_None(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := guardrailTestClient(t, g, true, true, nil)
	require.Equal(t, `getpet:{"id":1}`, callText(t, cs, "getpet", map[string]any{"id": 1}))
	require.Len(t, g.requests, 2)
}

// BLOCKED は理由付きで拒否し、pre_call ならバックエンドを呼ばない。
func TestGuardrailMiddleware_Blocked(t *testing.T) {
	blocked := func(guardrail.Request) (guardrail.Result, error) {
		return guardrail.Result{
			Action:        guardrail.ActionBlocked,
			BlockedReason: "contains secrets",
		}, nil
	}
	for _, tc := range []struct {
		name              string
		preCall, postCall bool
		wantBackendCalls  int32
	}{
		{name: "pre_call", preCall: true, wantBackendCalls: 0},
		{name: "post_call", postCall: true, wantBackendCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			cs := guardrailTestClient(
				t,
				&fakeGuardrail{fn: blocked},
				tc.preCall,
				tc.postCall,
				&calls,
			)
			_, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
				Name: "getpet", Arguments: map[string]any{"id": 1},
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "blocked by guardrail: contains secrets")
			require.Equal(t, tc.wantBackendCalls, calls.Load())
		})
	}
}

// ガードレールが判定できなければ拒否する（fail closed）。理由は呼び出し元に返さない。
func TestGuardrailMiddleware_FailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(guardrail.Request) (guardrail.Result, error)
	}{
		{name: "error", fn: func(guardrail.Request) (guardrail.Result, error) {
			return guardrail.Result{}, errors.New("guardrail down at 10.0.0.1")
		}},
		{name: "arguments rewritten into non-object", fn: func(guardrail.Request) (guardrail.Result, error) {
			return guardrail.Result{Action: guardrail.ActionIntervened, Texts: []string{`"x"`}}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			cs := guardrailTestClient(t, &fakeGuardrail{fn: tc.fn}, true, false, &calls)
			_, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
				Name: "getpet", Arguments: map[string]any{"id": 1},
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "guardrail check failed")
			require.NotContains(t, err.Error(), "10.0.0.1")
			require.Zero(t, calls.Load())
		})
	}
}

// post_call は StructuredContent も書き換え、テキスト以外の Content は通す。
func TestGuardrailMiddleware_PostCallStructuredContent(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{Name: "whoami", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.ImageContent{Data: []byte("img"), MIMEType: "image/png"},
					&mcp.TextContent{Text: "mail a@example.com"},
				},
				StructuredContent: map[string]any{"email": "a@example.com"},
			}, nil
		},
	)
	srv.AddReceivingMiddleware(NewGuardrailMiddleware("svc", "svc", g, false, true))
	cs := connectTestClient(t, t.Context(), srv)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "whoami"})
	require.NoError(t, err)
	require.Len(t, res.Content, 2)
	require.IsType(t, &mcp.ImageContent{}, res.Content[0])
	require.Equal(t, "mail [EMAIL]", res.Content[1].(*mcp.TextContent).Text)
	require.Equal(t, map[string]any{"email": "[EMAIL]"}, res.StructuredContent)
	require.Equal(
		t,
		[]string{"mail a@example.com", `{"email":"a@example.com"}`},
		g.requests[0].Texts,
	)
}

// tools/call 以外は素通しする。
func TestGuardrailMiddleware_OtherMethods(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := guardrailTestClient(t, g, true, true, nil)
	_, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Empty(t, g.requests)
}
