package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestAuditRedactor_Arguments(t *testing.T) {
	r, err := newAuditRedactor(config.AuditRedactConfig{
		Keys:     []string{"ssn"},
		Patterns: []string{`[\w.+-]+@[\w-]+\.[\w.]+`, `AKIA[0-9A-Z]{16}`},
	})
	require.NoError(t, err)

	got := r.redactArguments(json.RawMessage(`{
		"id": 42,
		"password": "hunter2",
		"githubToken": "ghp_x",
		"client_secret": "s",
		"X-Api-Key": "k",
		"max_tokens": 100,
		"user_ssn": "123-45-6789",
		"note": "mail a@example.com with AKIAABCDEFGHIJKLMNOP",
		"nested": [{"Authorization": "Bearer x", "name": "alice"}],
		"credentials": {"user": "u", "pass": "p"}
	}`))
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": 42,
		"password": "[REDACTED]",
		"githubToken": "[REDACTED]",
		"client_secret": "[REDACTED]",
		"X-Api-Key": "[REDACTED]",
		"max_tokens": 100,
		"user_ssn": "[REDACTED]",
		"note": "mail [REDACTED] with [REDACTED]",
		"nested": [{"Authorization": "[REDACTED]", "name": "alice"}],
		"credentials": "[REDACTED]"
	}`, string(raw))
	// 大きな数値の精度を落とさない。
	got = r.redactArguments(json.RawMessage(`{"n": 12345678901234567890}`))
	raw, err = json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, `{"n": 12345678901234567890}`, string(raw))

	// JSON として読めない引数は文字列として伏せる。
	require.Equal(
		t,
		"not json [REDACTED]",
		r.redactArguments(json.RawMessage(`not json a@example.com`)),
	)
}

func TestAuditRedactor_InvalidPattern(t *testing.T) {
	_, err := newAuditRedactor(config.AuditRedactConfig{Patterns: []string{"("}})
	require.Error(t, err)
}

// 監査ログには、伏せた引数とエラーメッセージが書かれる。
func TestAuditMiddleware_Redacts(t *testing.T) {
	var buf bytes.Buffer
	logger := newAuditLoggerTo(&buf, config.AuthzHeaders{}, true)
	redactor, err := newAuditRedactor(config.AuditRedactConfig{
		Patterns: []string{`[\w.+-]+@[\w-]+\.[\w.]+`},
	})
	require.NoError(t, err)
	logger.redactor = redactor
	fail := func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == authzMethodToolsCall {
				return nil, errors.New("backend rejected a@example.com")
			}
			return next(ctx, method, req)
		}
	}
	srv := newToolTestServer(t, nil, "getpet")
	srv.AddReceivingMiddleware(newAuditMiddleware("petstore", "pets", logger), fail)
	cs := connectTestClient(t, t.Context(), srv)
	_, err = cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "getpet", Arguments: map[string]any{"token": "t", "email": "a@example.com"},
	})
	require.Error(t, err)

	lines := decodeAuditLines(t, &buf)
	require.Len(t, lines, 1)
	require.Equal(
		t,
		map[string]any{"token": "[REDACTED]", "email": "[REDACTED]"},
		lines[0]["arguments"],
	)
	require.Equal(t, "backend rejected [REDACTED]", lines[0]["error"])
	require.NotContains(t, buf.String(), "a@example.com")
}
