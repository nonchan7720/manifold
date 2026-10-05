package mcpsrv

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/require"
)

// auditedCall calls create_invoice through audit -> authz (when d is
// non-nil) over HTTP with headers, and returns the single audit record.
func auditedCall(t *testing.T, d authz.Decider, headers http.Header) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	logger := newAuditLoggerTo(&buf, testAuthzHeaders(), false)
	srv := newBillingServer(t)
	mws := []mcp.Middleware{newAuditMiddleware("billing-svc", "billing", logger)}
	if d != nil {
		mws = append(mws, NewAuthzMiddleware("billing-svc", "billing", d, testAuthzHeaders(), nil))
	}
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	})
	httpSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(httpSrv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   httpSrv.URL,
		HTTPClient: &http.Client{Transport: &headerRoundTripper{headers: headers}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	_, _ = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	lines := decodeAuditLines(t, &buf)
	require.Len(t, lines, 1)
	return lines[0]
}

func TestAuditMiddleware_AuthzDecision(t *testing.T) {
	t.Run("allow", func(t *testing.T) {
		rec := auditedCall(t, &fakeDecider{allowResult: true}, identityHeaders())
		require.Equal(t, AuditAuthzAllow, rec["authz"])
		require.Equal(t, AuditOutcomeSuccess, rec["outcome"])
		require.Equal(t, "user-042", rec["user"])
		require.Equal(t, "header", rec["identity_source"])
	})
	t.Run("deny by policy", func(t *testing.T) {
		rec := auditedCall(t, &fakeDecider{allowResult: false}, identityHeaders())
		require.Equal(t, AuditAuthzDeny, rec["authz"])
		require.Equal(t, AuditOutcomeDenied, rec["outcome"])
	})
	t.Run("deny on decider error", func(t *testing.T) {
		rec := auditedCall(t, &fakeDecider{allowErr: errors.New("boom")}, identityHeaders())
		require.Equal(t, AuditAuthzDeny, rec["authz"])
	})
	t.Run("deny on missing identity", func(t *testing.T) {
		rec := auditedCall(t, &fakeDecider{allowResult: true}, http.Header{})
		require.Equal(t, AuditAuthzDeny, rec["authz"])
		require.NotContains(t, rec, "identity_source")
	})
	t.Run("bypass", func(t *testing.T) {
		rec := auditedCall(t, &fakeDecider{}, bypassHeaders())
		require.Equal(t, AuditAuthzBypass, rec["authz"])
		require.Equal(t, AuditOutcomeSuccess, rec["outcome"])
		require.NotContains(t, rec, "user")
	})
	t.Run("disabled", func(t *testing.T) {
		h := http.Header{}
		h.Set("x-user-id", "admin")
		rec := auditedCall(t, nil, h)
		require.Equal(t, AuditAuthzDisabled, rec["authz"])
		require.Equal(t, "admin", rec["user"])
		require.Equal(t, "header", rec["identity_source"])
	})
}
