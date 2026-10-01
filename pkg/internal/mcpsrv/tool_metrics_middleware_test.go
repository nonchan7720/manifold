package mcpsrv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
	"github.com/stretchr/testify/require"
)

// fakeToolMetricsRecorder collects every recorded event.
type fakeToolMetricsRecorder struct {
	mu     sync.Mutex
	events []toolmetrics.Event
}

func (r *fakeToolMetricsRecorder) Record(e toolmetrics.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *fakeToolMetricsRecorder) recorded() []toolmetrics.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]toolmetrics.Event(nil), r.events...)
}

// newToolMetricsTestServer serves srv with mws as receiving middleware
// (first is outermost, as in pkg/cmd/server.go) and returns a connected
// session sending headers on every request.
func newToolMetricsTestServer(
	t *testing.T, srv *mcp.Server, headers http.Header, mws ...mcp.Middleware,
) *mcp.ClientSession {
	t.Helper()
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

func newToolMetricsBillingServer(t *testing.T) *mcp.Server {
	t.Helper()
	srv := newBillingServer(t)
	srv.AddTool(
		&mcp.Tool{Name: "void_invoice", InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "upstream returned 409"}},
			}, nil
		},
	)
	return srv
}

func TestToolMetricsMiddleware_Success(t *testing.T) {
	rec := &fakeToolMetricsRecorder{}
	session := newToolMetricsTestServer(t, newToolMetricsBillingServer(t), identityHeaders(),
		NewToolMetricsMiddleware("billing-svc", "billing", "x-user-id", rec))

	_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_invoice"})
	require.NoError(t, err)

	events := rec.recorded()
	require.Len(t, events, 1)
	e := events[0]
	require.NotEmpty(t, e.ID)
	require.False(t, e.Timestamp.IsZero())
	require.Equal(t, "billing-svc", e.Server)
	require.Equal(t, "billing", e.Service)
	require.Equal(t, "create_invoice", e.Tool)
	require.Equal(t, "user-042", e.User)
	require.Equal(t, toolmetrics.StatusSuccess, e.Status)
	require.Zero(t, e.ErrorCode)
	require.Empty(t, e.ErrorMessage)
}

func TestToolMetricsMiddleware_ToolError_RecordsResultText(t *testing.T) {
	rec := &fakeToolMetricsRecorder{}
	session := newToolMetricsTestServer(t, newToolMetricsBillingServer(t), http.Header{},
		NewToolMetricsMiddleware("billing-svc", "billing", "x-user-id", rec))

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "void_invoice"})
	require.NoError(t, err)
	require.True(t, result.IsError)

	events := rec.recorded()
	require.Len(t, events, 1)
	require.Equal(t, toolmetrics.StatusToolError, events[0].Status)
	require.Equal(t, "upstream returned 409", events[0].ErrorMessage)
	require.Empty(t, events[0].User, "no user header was sent")
}

func TestToolMetricsMiddleware_AuthzDenied_RecordedAsError(t *testing.T) {
	rec := &fakeToolMetricsRecorder{}
	d := &fakeDecider{allowResult: false}
	session := newToolMetricsTestServer(t, newToolMetricsBillingServer(t), identityHeaders(),
		NewToolMetricsMiddleware("billing-svc", "billing", "x-user-id", rec),
		NewAuthzMiddleware("billing-svc", "billing", d, testAuthzHeaders(), nil),
	)

	_, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "delete_invoice"})
	require.Error(t, err)

	events := rec.recorded()
	require.Len(t, events, 1)
	require.Equal(t, "delete_invoice", events[0].Tool)
	require.Equal(t, toolmetrics.StatusError, events[0].Status)
	require.Equal(t, int64(jsonrpc.CodeInternalError), events[0].ErrorCode)
	require.Equal(t, "tool not allowed by policy", events[0].ErrorMessage)
}

func TestToolMetricsMiddleware_IgnoresOtherMethods(t *testing.T) {
	rec := &fakeToolMetricsRecorder{}
	session := newToolMetricsTestServer(t, newToolMetricsBillingServer(t), http.Header{},
		NewToolMetricsMiddleware("billing-svc", "billing", "", rec))

	_, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Empty(t, rec.recorded())
}

func TestSetToolMetricsStatus_NonJSONRPCError(t *testing.T) {
	var e toolmetrics.Event
	setToolMetricsStatus(&e, nil, errors.New("connection refused"))
	require.Equal(t, toolmetrics.StatusError, e.Status)
	require.Zero(t, e.ErrorCode)
	require.Equal(t, "connection refused", e.ErrorMessage)
}

func TestSetToolMetricsStatus_TruncatesLongMessage(t *testing.T) {
	var e toolmetrics.Event
	setToolMetricsStatus(
		&e,
		nil,
		errors.New(strings.Repeat("x", toolmetrics.MaxErrorMessageLength+10)),
	)
	require.Len(t, e.ErrorMessage, toolmetrics.MaxErrorMessageLength)
}

func TestTruncateUTF8_DoesNotSplitRune(t *testing.T) {
	// "あ" は 3 バイト。4 バイトで切ると 2 文字目の途中になるため 1 文字に丸める。
	require.Equal(t, "あ", truncateUTF8("ああ", 4))
	require.Equal(t, "ああ", truncateUTF8("ああ", 6))
	require.Equal(t, "ab", truncateUTF8("abc", 2))
}
