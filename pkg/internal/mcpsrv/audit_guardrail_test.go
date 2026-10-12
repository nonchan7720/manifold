package mcpsrv

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/services/guardrail"
	"github.com/stretchr/testify/require"
)

// fakeGuardrail は呼び出しを記録し、固定の結果を返す Guardrail。release が
// あれば、それが閉じられるまで Apply を止める。
type fakeGuardrail struct {
	mu       sync.Mutex
	requests []guardrail.Request
	result   guardrail.Result
	err      error
	release  chan struct{}
}

func (g *fakeGuardrail) Apply(_ context.Context, req guardrail.Request) (guardrail.Result, error) {
	if g.release != nil {
		<-g.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests = append(g.requests, req)
	return g.result, g.err
}

// auditWithGuardrail は g を通す監査ログで getpet を1回呼び、Close 後の記録を返す。
func auditWithGuardrail(t *testing.T, g *fakeGuardrail) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	logger := newAuditLoggerTo(&buf, config.AuthzHeaders{}, true)
	logger.startGuardrail(g, 4, 2)
	srv := newToolTestServer(t, nil, "getpet")
	srv.AddReceivingMiddleware(newAuditMiddleware("petstore", "pets", logger))
	cs := connectTestClient(t, t.Context(), srv)
	callText(t, cs, "getpet", map[string]any{"email": "a@example.com"})
	require.NoError(t, logger.Close())
	return decodeAuditLines(t, &buf)
}

func TestAuditGuardrail_Masked(t *testing.T) {
	g := &fakeGuardrail{result: guardrail.Result{
		Action: guardrail.ActionIntervened,
		Texts:  []string{`{"email":"[EMAIL]"}`},
	}}
	lines := auditWithGuardrail(t, g)
	require.Len(t, lines, 1)
	require.Equal(t, map[string]any{"email": "[EMAIL]"}, lines[0]["arguments"])
	require.Equal(t, AuditGuardrailMasked, lines[0]["arguments_guardrail"])
	require.Equal(t, "getpet", lines[0]["tool"])

	require.Len(t, g.requests, 1)
	require.JSONEq(t, `{"email":"a@example.com"}`, g.requests[0].Texts[0])
	require.Equal(t, guardrail.InputTypeRequest, g.requests[0].InputType)
	require.Equal(t, "petstore", g.requests[0].Params["server"])
	require.Equal(t, "getpet", g.requests[0].Params["tool"])
}

// 書き換え後が JSON でなくなった場合は文字列として記録する。
func TestAuditGuardrail_MaskedNotJSON(t *testing.T) {
	g := &fakeGuardrail{result: guardrail.Result{
		Action: guardrail.ActionIntervened,
		Texts:  []string{`{"phone":{PHONE}}`},
	}}
	lines := auditWithGuardrail(t, g)
	require.Equal(t, `{"phone":{PHONE}}`, lines[0]["arguments"])
	require.Equal(t, AuditGuardrailMasked, lines[0]["arguments_guardrail"])
}

func TestAuditGuardrail_None(t *testing.T) {
	g := &fakeGuardrail{result: guardrail.Result{Action: guardrail.ActionNone}}
	lines := auditWithGuardrail(t, g)
	require.Equal(t, map[string]any{"email": "a@example.com"}, lines[0]["arguments"])
	require.Equal(t, AuditGuardrailNone, lines[0]["arguments_guardrail"])
}

// 拒否・失敗では引数を記録しない。
func TestAuditGuardrail_BlockedOrError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		g          *fakeGuardrail
		wantStatus string
		wantReason any
	}{
		{
			name: "blocked",
			g: &fakeGuardrail{result: guardrail.Result{
				Action: guardrail.ActionBlocked, BlockedReason: "contains secrets",
			}},
			wantStatus: AuditGuardrailBlocked,
			wantReason: "contains secrets",
		},
		{
			name:       "error",
			g:          &fakeGuardrail{err: errors.New("guardrail down")},
			wantStatus: AuditGuardrailError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := auditWithGuardrail(t, tc.g)
			require.Len(t, lines, 1)
			require.NotContains(t, lines[0], "arguments")
			require.Equal(t, tc.wantStatus, lines[0]["arguments_guardrail"])
			require.Equal(t, tc.wantReason, lines[0]["arguments_guardrail_reason"])
			require.Equal(t, AuditOutcomeSuccess, lines[0]["outcome"])
		})
	}
}

// ガードレールが詰まっても tools/call は待たず、溢れた記録は引数なしで書く。
func TestAuditGuardrail_QueueFullSkipsArguments(t *testing.T) {
	g := &fakeGuardrail{
		result:  guardrail.Result{Action: guardrail.ActionNone},
		release: make(chan struct{}),
	}
	var buf bytes.Buffer
	logger := newAuditLoggerTo(&buf, config.AuthzHeaders{}, true)
	logger.startGuardrail(g, 1, 1)
	srv := newToolTestServer(t, nil, "getpet")
	srv.AddReceivingMiddleware(newAuditMiddleware("petstore", "pets", logger))
	cs := connectTestClient(t, t.Context(), srv)

	call := func() {
		_, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "getpet", Arguments: map[string]any{"id": 1},
		})
		require.NoError(t, err)
	}
	// 1件目はワーカーが受け取って止まる。2件目がキュー（1件）を埋め、残りは溢れる。
	// ガードレールが止まっていても tools/call は待たずに返る。
	call()
	require.Eventually(t, func() bool { return len(logger.queue) == 0 },
		5*time.Second, 10*time.Millisecond)
	for range 4 {
		call()
	}
	close(g.release)
	require.NoError(t, logger.Close())

	lines := decodeAuditLines(t, &buf)
	require.Len(t, lines, 5)
	var skipped, guarded int
	for _, line := range lines {
		switch line["arguments_guardrail"] {
		case AuditGuardrailSkipped:
			skipped++
			require.NotContains(t, line, "arguments")
		case AuditGuardrailNone:
			guarded++
		}
	}
	require.Equal(t, 2, guarded)
	require.Equal(t, 3, skipped)
}

// Close の後に届いた記録は、ガードレールを通さず引数なしで書く。
func TestAuditGuardrail_AfterClose(t *testing.T) {
	g := &fakeGuardrail{result: guardrail.Result{Action: guardrail.ActionNone}}
	var buf bytes.Buffer
	logger := newAuditLoggerTo(&buf, config.AuthzHeaders{}, true)
	logger.startGuardrail(g, 4, 1)
	require.NoError(t, logger.Close())
	require.NoError(t, logger.Close())

	srv := newToolTestServer(t, nil, "getpet")
	srv.AddReceivingMiddleware(newAuditMiddleware("petstore", "pets", logger))
	cs := connectTestClient(t, t.Context(), srv)
	callText(t, cs, "getpet", map[string]any{"id": 1})

	lines := decodeAuditLines(t, &buf)
	require.Len(t, lines, 1)
	require.Equal(t, AuditGuardrailSkipped, lines[0]["arguments_guardrail"])
	require.NotContains(t, lines[0], "arguments")
	require.Empty(t, g.requests)
}
