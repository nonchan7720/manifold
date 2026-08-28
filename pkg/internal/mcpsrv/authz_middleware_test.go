package mcpsrv

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/require"
)

// fakeDecider is a Decider test double recording every call it receives.
type fakeDecider struct {
	mu sync.Mutex

	allowResult bool
	allowErr    error
	allowCalls  []struct {
		p authz.Principal
		t authz.ToolRef
	}

	allowedToolsResult []authz.ToolRef
	allowedToolsErr    error
	allowedToolsCalls  [][]authz.ToolRef

	allowCatalogResult bool
	allowCatalogErr    error
}

func (d *fakeDecider) Allow(
	_ context.Context, p authz.Principal, t authz.ToolRef,
) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.allowCalls = append(d.allowCalls, struct {
		p authz.Principal
		t authz.ToolRef
	}{p, t})
	return d.allowResult, d.allowErr
}

func (d *fakeDecider) AllowedTools(
	_ context.Context, _ authz.Principal, tools []authz.ToolRef,
) ([]authz.ToolRef, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.allowedToolsCalls = append(d.allowedToolsCalls, tools)
	if d.allowedToolsErr != nil {
		return nil, d.allowedToolsErr
	}
	return d.allowedToolsResult, nil
}

func (d *fakeDecider) AllowCatalog(_ context.Context, _ authz.Principal) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.allowCatalogResult, d.allowCatalogErr
}

func (d *fakeDecider) allowCallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.allowCalls)
}

func (d *fakeDecider) allowedToolsCallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.allowedToolsCalls)
}

func testAuthzHeaders() config.AuthzHeaders {
	return config.AuthzHeaders{
		UserID:     "x-user-id",
		UserGroups: "x-user-groups",
		Bypass:     "x-authz-bypass",
	}
}

// headerRoundTripper injects a fixed set of headers into every outgoing
// request, standing in for the identity headers an upstream proxy would add
// (StreamableClientTransport otherwise has no way to set them).
type headerRoundTripper struct {
	headers http.Header
}

func (rt *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, vs := range rt.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}

// newAuthzTestServer wires srv behind the same StreamableHTTPHandler +
// authz middleware stack pkg/cmd/server.go builds, and returns a connected
// client session using headers as the request's identity headers.
func newAuthzTestServer(
	t *testing.T,
	srv *mcp.Server,
	d authz.Decider,
	headers http.Header,
) *mcp.ClientSession {
	t.Helper()
	srv.AddReceivingMiddleware(NewAuthzMiddleware("billing-svc", d, testAuthzHeaders()))

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
	return session
}

func newBillingServer(t *testing.T) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "billing-svc", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{
			Name:        "create_invoice",
			Description: "create an invoice",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "invoice-created"}},
			}, nil
		},
	)
	srv.AddTool(
		&mcp.Tool{
			Name:        "delete_invoice",
			Description: "delete an invoice",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "invoice-deleted"}},
			}, nil
		},
	)
	return srv
}

func identityHeaders() http.Header {
	h := http.Header{}
	h.Set("x-user-id", "user-042")
	h.Set("x-user-groups", "team-finance,team-ops")
	return h
}

// --- tools/call ---

func TestAuthzMiddleware_ToolCall_Allowed_ReachesUpstream(t *testing.T) {
	d := &fakeDecider{allowResult: true}
	session := newAuthzTestServer(t, newBillingServer(t), d, identityHeaders())

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "invoice-created", text.Text)

	require.Equal(t, 1, d.allowCallCount())
	require.Equal(t, "user-042", d.allowCalls[0].p.UserID)
	require.Equal(t, []string{"team-finance", "team-ops"}, d.allowCalls[0].p.Groups)
	require.Equal(
		t,
		authz.ToolRef{Server: "billing-svc", Name: "create_invoice"},
		d.allowCalls[0].t,
	)
}

func TestAuthzMiddleware_ToolCall_Denied_ReturnsFixedJSONRPCError(t *testing.T) {
	d := &fakeDecider{allowResult: false}
	session := newAuthzTestServer(t, newBillingServer(t), d, identityHeaders())

	_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "delete_invoice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Equal(t, 1, d.allowCallCount())
}

func TestAuthzMiddleware_ToolCall_DeciderError_ReturnsFixedJSONRPCError(t *testing.T) {
	d := &fakeDecider{allowErr: context.DeadlineExceeded}
	session := newAuthzTestServer(t, newBillingServer(t), d, identityHeaders())

	_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.NotContains(
		t, err.Error(), "deadline exceeded",
		"the decider's error detail must not reach the client",
	)
}

func TestAuthzMiddleware_ToolCall_MissingIdentityHeader_DeniesWithoutCallingDecider(t *testing.T) {
	d := &fakeDecider{allowResult: true}
	session := newAuthzTestServer(t, newBillingServer(t), d, http.Header{})

	_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Equal(t, 0, d.allowCallCount())
}

// authzHandleToolCall is exercised directly here (rather than through a real
// tools/call request) because the go-sdk only ever hands it *CallToolParamsRaw
// in practice; building a *ServerRequest with a mismatched Params type is the
// only way to reach the defensive branch.
func TestAuthzHandleToolCall_UnexpectedParamsType_DeniesWithoutCallingDeciderOrNext(t *testing.T) {
	d := &fakeDecider{allowResult: true}
	nextCalled := false
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return &mcp.CallToolResult{}, nil
	}
	req := &mcp.ServerRequest[*mcp.ListToolsParams]{Params: &mcp.ListToolsParams{}}
	p := authz.Principal{UserID: "user-042", Groups: []string{"team-finance"}}

	_, err := authzHandleToolCall(
		t.Context(), "billing-svc", d, p, next, "tools/call", req,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.False(t, nextCalled)
	require.Equal(t, 0, d.allowCallCount())
}

// --- tools/list ---

func TestAuthzMiddleware_ToolsList_FiltersUsingSingleAllowedToolsCall(t *testing.T) {
	d := &fakeDecider{
		allowedToolsResult: []authz.ToolRef{{Server: "billing-svc", Name: "create_invoice"}},
	}
	session := newAuthzTestServer(t, newBillingServer(t), d, identityHeaders())

	result, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, d.allowedToolsCallCount())
	require.Len(t, result.Tools, 1)
	require.Equal(t, "create_invoice", result.Tools[0].Name)
}

func TestAuthzMiddleware_ToolsList_DeciderError_ReturnsFixedJSONRPCError(t *testing.T) {
	d := &fakeDecider{allowedToolsErr: context.DeadlineExceeded}
	session := newAuthzTestServer(t, newBillingServer(t), d, identityHeaders())

	_, err := session.ListTools(t.Context(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
}

func TestAuthzMiddleware_ToolsList_EmptyTools_DoesNotCallDecider(t *testing.T) {
	d := &fakeDecider{}
	srv := mcp.NewServer(&mcp.Implementation{Name: "billing-svc", Version: "0.0.1"}, nil)
	session := newAuthzTestServer(t, srv, d, identityHeaders())

	result, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Empty(t, result.Tools)
	require.Equal(t, 0, d.allowedToolsCallCount())
}

func TestAuthzMiddleware_ToolsList_MissingIdentityHeader_DeniesWithoutCallingDecider(t *testing.T) {
	d := &fakeDecider{
		allowedToolsResult: []authz.ToolRef{{Server: "billing-svc", Name: "create_invoice"}},
	}
	session := newAuthzTestServer(t, newBillingServer(t), d, http.Header{})

	_, err := session.ListTools(t.Context(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Equal(t, 0, d.allowedToolsCallCount())
}

// --- other methods pass through ---

func TestAuthzMiddleware_OtherMethod_PassesThrough(t *testing.T) {
	d := &fakeDecider{}
	session := newAuthzTestServer(t, newBillingServer(t), d, http.Header{})

	_, err := session.ListResources(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, 0, d.allowCallCount())
	require.Equal(t, 0, d.allowedToolsCallCount())
}

// --- missing Extra (in-memory transport never sets RequestExtra) ---

func TestAuthzMiddleware_NoExtra_DeniesWithoutCallingDecider(t *testing.T) {
	d := &fakeDecider{allowResult: true}
	srv := newBillingServer(t)
	srv.AddReceivingMiddleware(NewAuthzMiddleware("billing-svc", d, testAuthzHeaders()))

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err := srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	defer session.Close() //nolint: errcheck

	_, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Equal(t, 0, d.allowCallCount())
}

// --- bypass ---

func bypassHeaders() http.Header {
	h := http.Header{}
	h.Set("x-authz-bypass", "true")
	return h
}

func TestAuthzMiddleware_ToolCall_BypassHeaderTrue_ReachesUpstreamWithoutDecider(t *testing.T) {
	d := &fakeDecider{allowResult: false}
	session := newAuthzTestServer(t, newBillingServer(t), d, bypassHeaders())

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, 0, d.allowCallCount())
}

func TestAuthzMiddleware_ToolCall_BypassHeaderNotExactlyTrue_UsesNormalAuthz(t *testing.T) {
	d := &fakeDecider{allowResult: true}
	headers := http.Header{}
	headers.Set("x-authz-bypass", "True")
	session := newAuthzTestServer(t, newBillingServer(t), d, headers)

	_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Equal(t, 0, d.allowCallCount())
}

func TestAuthzMiddleware_ToolsList_BypassHeaderTrue_ReturnsUnfilteredListWithoutDecider(
	t *testing.T,
) {
	d := &fakeDecider{allowedToolsResult: nil}
	session := newAuthzTestServer(t, newBillingServer(t), d, bypassHeaders())

	result, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, result.Tools, 2)
	require.Equal(t, 0, d.allowedToolsCallCount())
}

func TestAuthzMiddleware_ToolCall_BypassHeaderTrue_LogsBypassDecision(t *testing.T) {
	var buf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	d := &fakeDecider{allowResult: false}
	session := newAuthzTestServer(t, newBillingServer(t), d, bypassHeaders())

	_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.NoError(t, err)

	logOutput := buf.String()
	require.Contains(t, logOutput, "decision=bypass")
	require.Contains(t, logOutput, "server=billing-svc")
	require.Contains(t, logOutput, "method=tools/call")
	require.NotContains(t, logOutput, "user=")
}
