package mcpsrv

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/require"
)

func testToolScopeHeaders() config.ToolScopeHeaders {
	return config.ToolScopeHeaders{
		Services: "x-tool-scope-services",
		Servers:  "x-tool-scope-servers",
	}
}

// newToolScopeTestServer wires srv (server "billing-svc" of service
// "billing") behind the tool scope middleware and, when d is non-nil, the
// authz middleware inside it — the same order pkg/cmd/server.go builds.
func newToolScopeTestServer(
	t *testing.T, srv *mcp.Server, d *fakeDecider, headers http.Header,
) *mcp.ClientSession {
	t.Helper()
	var mws []mcp.Middleware
	mws = append(mws, NewToolScopeMiddleware("billing-svc", "billing", testToolScopeHeaders()))
	if d != nil {
		mws = append(mws, NewAuthzMiddleware("billing-svc", "billing", d, testAuthzHeaders(), nil))
	}
	srv.AddReceivingMiddleware(mws...)

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

func scopeHeaders(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

// --- toolScopeAllows ---

func TestToolScopeAllows(t *testing.T) {
	headers := testToolScopeHeaders()
	tests := []struct {
		name        string
		header      http.Header
		wantAllowed bool
		wantScoped  bool
	}{
		{"no headers", http.Header{}, true, false},
		{"service listed", scopeHeaders("x-tool-scope-services", "billing"), true, true},
		{
			"service listed among others with spaces",
			scopeHeaders("x-tool-scope-services", "accounting-1 , billing,"), true, true,
		},
		{"service not listed", scopeHeaders("x-tool-scope-services", "accounting-1"), false, true},
		{"service header empty", scopeHeaders("x-tool-scope-services", ""), false, true},
		{
			"service listed in a repeated header",
			scopeHeaders(
				"x-tool-scope-services",
				"accounting-1",
				"x-tool-scope-services",
				"billing",
			),
			true,
			true,
		},
		{"server listed", scopeHeaders("x-tool-scope-servers", "billing-svc"), true, true},
		{"server not listed", scopeHeaders("x-tool-scope-servers", "billing-api"), false, true},
		{
			"service name is not a server name",
			scopeHeaders("x-tool-scope-servers", "billing"), false, true,
		},
		{
			"both listed",
			scopeHeaders("x-tool-scope-services", "billing", "x-tool-scope-servers", "billing-svc"),
			true, true,
		},
		{
			"service listed but server not",
			scopeHeaders("x-tool-scope-services", "billing", "x-tool-scope-servers", "billing-api"),
			false, true,
		},
		{"match is case-sensitive", scopeHeaders("x-tool-scope-services", "Billing"), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, scoped := toolScopeAllows(tt.header, headers, "billing-svc", "billing")
			require.Equal(t, tt.wantAllowed, allowed)
			require.Equal(t, tt.wantScoped, scoped)
		})
	}
}

// --- tools/list ---

func TestToolScopeMiddleware_ToolsList_NoHeader_ReturnsFullList(t *testing.T) {
	session := newToolScopeTestServer(t, newBillingServer(t), nil, http.Header{})

	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"create_invoice", "delete_invoice"}, toolNames(res.Tools))
	require.Equal(t, "public", res.CacheScope, "an unscoped list keeps the SDK's default")
}

func TestToolScopeMiddleware_ToolsList_InScope_ReturnsFullListAsPrivate(t *testing.T) {
	session := newToolScopeTestServer(t, newBillingServer(t), nil,
		scopeHeaders("x-tool-scope-services", "billing"))

	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"create_invoice", "delete_invoice"}, toolNames(res.Tools))
	require.Equal(t, "private", res.CacheScope)
	require.Equal(t, 0, res.TTLMs)
}

func TestToolScopeMiddleware_ToolsList_OutOfScope_ReturnsEmptyListWithoutAuthz(t *testing.T) {
	d := &fakeDecider{}
	headers := identityHeaders()
	headers.Set("x-tool-scope-services", "accounting-1")
	session := newToolScopeTestServer(t, newBillingServer(t), d, headers)

	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Empty(t, res.Tools)
	require.Equal(t, "private", res.CacheScope)
	require.Equal(t, 0, d.allowedToolsCallCount(), "an out-of-scope server must not query OPA")
}

func TestToolScopeMiddleware_ToolsList_InScope_StillFilteredByAuthz(t *testing.T) {
	d := &fakeDecider{allowedToolsResult: []authz.ToolRef{
		{Server: "billing-svc", Service: "billing", Name: "create_invoice"},
	}}
	headers := identityHeaders()
	headers.Set("x-tool-scope-services", "billing")
	session := newToolScopeTestServer(t, newBillingServer(t), d, headers)

	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"create_invoice"}, toolNames(res.Tools))
	require.Equal(t, 1, d.allowedToolsCallCount())
}

// --- tools/call ---

func TestToolScopeMiddleware_ToolCall_InScope_ReachesUpstream(t *testing.T) {
	session := newToolScopeTestServer(t, newBillingServer(t), nil,
		scopeHeaders("x-tool-scope-servers", "billing-svc"))

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.NoError(t, err)
	require.False(t, result.IsError)
}

func TestToolScopeMiddleware_ToolCall_OutOfScope_RejectedWithoutAuthz(t *testing.T) {
	d := &fakeDecider{allowResult: true}
	headers := identityHeaders()
	headers.Set("x-tool-scope-servers", "billing-api")
	session := newToolScopeTestServer(t, newBillingServer(t), d, headers)

	_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not enabled in this request's tool scope")
	require.Equal(t, 0, d.allowCallCount(), "an out-of-scope server must not query OPA")
}

func TestToolScopeMiddleware_ToolCall_InScope_StillDeniedByAuthz(t *testing.T) {
	d := &fakeDecider{allowResult: false}
	headers := identityHeaders()
	headers.Set("x-tool-scope-services", "billing")
	session := newToolScopeTestServer(t, newBillingServer(t), d, headers)

	_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Equal(t, 1, d.allowCallCount())
}

func TestToolScopeMiddleware_NoExtra_PassesThrough(t *testing.T) {
	srv := newBillingServer(t)
	srv.AddReceivingMiddleware(
		NewToolScopeMiddleware("billing-svc", "billing", testToolScopeHeaders()),
	)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err := srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	defer session.Close() //nolint: errcheck

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.NoError(t, err)
	require.False(t, result.IsError)
}
