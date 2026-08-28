package authz

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func testPrincipal() Principal {
	return Principal{UserID: "user-042", Groups: []string{"team-finance", "team-ops"}}
}

func testAuthzConfig(opaURL string) config.AuthzConfig {
	return config.AuthzConfig{
		Enabled: true,
		OPAURL:  opaURL,
		Timeout: 2 * time.Second,
		DecisionPath: config.AuthzDecisionPath{
			List:    "/v1/data/acme/authz/allowed_tools",
			Call:    "/v1/data/acme/authz/allow",
			Catalog: "/v1/data/acme/authz/allow_catalog",
		},
	}
}

type capturedRequest struct {
	path        string
	contentType string
	body        map[string]any
}

// opaStub is a stub OPA endpoint recording the last request it received and
// replying with a fixed JSON body (or a fixed status for error-path tests).
type opaStub struct {
	srv *httptest.Server

	calls atomic.Int32

	response   string
	statusCode int
	delay      time.Duration

	lastRequest atomic.Pointer[capturedRequest]
}

func newOPAStub(t *testing.T) *opaStub {
	t.Helper()
	s := &opaStub{statusCode: http.StatusOK}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)

		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.lastRequest.Store(&capturedRequest{
			path:        r.URL.Path,
			contentType: r.Header.Get("Content-Type"),
			body:        body,
		})

		if s.delay > 0 {
			time.Sleep(s.delay)
		}
		if s.statusCode != http.StatusOK {
			w.WriteHeader(s.statusCode)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(s.response))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// --- Allow: request shape ---

func TestOPADecider_Allow_PostsToCallDecisionPath(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": true}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.Allow(
		t.Context(),
		testPrincipal(),
		ToolRef{Server: "billing-svc", Name: "create_invoice"},
	)
	require.NoError(t, err)

	req := s.lastRequest.Load()
	require.NotNil(t, req)
	require.Equal(t, "/v1/data/acme/authz/allow", req.path)
	require.Equal(t, "application/json", req.contentType)

	input, ok := req.body["input"].(map[string]any)
	require.True(t, ok, "body must have an 'input' object")
	require.Equal(t, "user-042", input["user"])
	require.Equal(t, []any{"team-finance", "team-ops"}, input["groups"])
	require.Equal(t, "billing-svc", input["server"])
	require.Equal(t, "create_invoice", input["tool"])
}

func TestOPADecider_Allow_CustomInputKeys_PostsWithConfiguredNames(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": true}`
	cfg := testAuthzConfig(s.srv.URL)
	cfg.Input = config.AuthzInput{
		User:   "acct",
		Groups: "roles",
		Server: "svc",
		Tool:   "action",
	}
	d := NewOPADecider(cfg, nil)

	_, err := d.Allow(
		t.Context(),
		testPrincipal(),
		ToolRef{Server: "billing-svc", Name: "create_invoice"},
	)
	require.NoError(t, err)

	req := s.lastRequest.Load()
	input, ok := req.body["input"].(map[string]any)
	require.True(t, ok, "body must have an 'input' object")
	require.Equal(t, "user-042", input["acct"])
	require.Equal(t, []any{"team-finance", "team-ops"}, input["roles"])
	require.Equal(t, "billing-svc", input["svc"])
	require.Equal(t, "create_invoice", input["action"])
	require.NotContains(t, input, "user")
	require.NotContains(t, input, "groups")
	require.NotContains(t, input, "server")
	require.NotContains(t, input, "tool")
}

// --- Allow: result handling ---

func TestOPADecider_Allow_ResultTrue(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": true}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	allowed, err := d.Allow(
		t.Context(),
		testPrincipal(),
		ToolRef{Server: "billing-svc", Name: "create_invoice"},
	)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestOPADecider_Allow_ResultFalse(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": false}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	allowed, err := d.Allow(
		t.Context(),
		testPrincipal(),
		ToolRef{Server: "billing-svc", Name: "create_invoice"},
	)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestOPADecider_Allow_ResultMissing_Error(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.Allow(
		t.Context(),
		testPrincipal(),
		ToolRef{Server: "billing-svc", Name: "create_invoice"},
	)
	require.Error(t, err)
}

func TestOPADecider_Allow_ResultNonBool_Error(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": "yes"}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.Allow(
		t.Context(),
		testPrincipal(),
		ToolRef{Server: "billing-svc", Name: "create_invoice"},
	)
	require.Error(t, err)
}

func TestOPADecider_Allow_NonOKStatus_Error(t *testing.T) {
	s := newOPAStub(t)
	s.statusCode = http.StatusInternalServerError
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.Allow(
		t.Context(),
		testPrincipal(),
		ToolRef{Server: "billing-svc", Name: "create_invoice"},
	)
	require.Error(t, err)
}

func TestOPADecider_Allow_Timeout_Error(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": true}`
	s.delay = 100 * time.Millisecond
	cfg := testAuthzConfig(s.srv.URL)
	cfg.Timeout = 10 * time.Millisecond
	d := NewOPADecider(cfg, nil)

	_, err := d.Allow(
		t.Context(),
		testPrincipal(),
		ToolRef{Server: "billing-svc", Name: "create_invoice"},
	)
	require.Error(t, err)
}

func TestOPADecider_Allow_ConnectionFailure_Error(t *testing.T) {
	s := newOPAStub(t)
	s.srv.Close()
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.Allow(
		t.Context(),
		testPrincipal(),
		ToolRef{Server: "billing-svc", Name: "create_invoice"},
	)
	require.Error(t, err)
}

// --- AllowedTools: request shape ---

func TestOPADecider_AllowedTools_PostsToListDecisionPath(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": []}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	tools := []ToolRef{
		{Server: "billing-svc", Name: "create_invoice"},
		{Server: "inventory-svc", Name: "list_items"},
	}
	_, err := d.AllowedTools(t.Context(), testPrincipal(), tools)
	require.NoError(t, err)

	req := s.lastRequest.Load()
	require.NotNil(t, req)
	require.Equal(t, "/v1/data/acme/authz/allowed_tools", req.path)

	input, ok := req.body["input"].(map[string]any)
	require.True(t, ok, "body must have an 'input' object")
	require.Equal(t, "user-042", input["user"])
	gotTools, ok := input["tools"].([]any)
	require.True(t, ok)
	require.Equal(t, []any{
		map[string]any{"server": "billing-svc", "name": "create_invoice"},
		map[string]any{"server": "inventory-svc", "name": "list_items"},
	}, gotTools)
}

func TestOPADecider_AllowedTools_CustomInputKeys_PostsWithConfiguredNames(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": []}`
	cfg := testAuthzConfig(s.srv.URL)
	cfg.Input = config.AuthzInput{
		User:     "acct",
		Groups:   "roles",
		Server:   "svc",
		Tools:    "items",
		ToolName: "op",
	}
	d := NewOPADecider(cfg, nil)

	tools := []ToolRef{{Server: "billing-svc", Name: "create_invoice"}}
	_, err := d.AllowedTools(t.Context(), testPrincipal(), tools)
	require.NoError(t, err)

	req := s.lastRequest.Load()
	input, ok := req.body["input"].(map[string]any)
	require.True(t, ok, "body must have an 'input' object")
	require.Equal(t, "user-042", input["acct"])
	require.Equal(t, []any{"team-finance", "team-ops"}, input["roles"])
	gotTools, ok := input["items"].([]any)
	require.True(t, ok)
	require.Equal(t, []any{
		map[string]any{"svc": "billing-svc", "op": "create_invoice"},
	}, gotTools)
	require.NotContains(t, input, "user")
	require.NotContains(t, input, "groups")
	require.NotContains(t, input, "tools")
}

// --- AllowedTools: result handling ---

func TestOPADecider_AllowedTools_FiltersAndPreservesInputOrder(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": [
		{"server": "inventory-svc", "name": "list_items"},
		{"server": "billing-svc", "name": "create_invoice"}
	]}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	tools := []ToolRef{
		{Server: "billing-svc", Name: "create_invoice"},
		{Server: "billing-svc", Name: "delete_invoice"},
		{Server: "inventory-svc", Name: "list_items"},
	}
	got, err := d.AllowedTools(t.Context(), testPrincipal(), tools)
	require.NoError(t, err)
	require.Equal(t, []ToolRef{
		{Server: "billing-svc", Name: "create_invoice"},
		{Server: "inventory-svc", Name: "list_items"},
	}, got)
}

func TestOPADecider_AllowedTools_ReadsResultWithConfiguredKeys(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": [{"svc": "billing-svc", "op": "create_invoice"}]}`
	cfg := testAuthzConfig(s.srv.URL)
	cfg.Input = config.AuthzInput{Server: "svc", ToolName: "op"}
	d := NewOPADecider(cfg, nil)

	tools := []ToolRef{
		{Server: "billing-svc", Name: "create_invoice"},
		{Server: "billing-svc", Name: "delete_invoice"},
	}
	got, err := d.AllowedTools(t.Context(), testPrincipal(), tools)
	require.NoError(t, err)
	require.Equal(t, []ToolRef{{Server: "billing-svc", Name: "create_invoice"}}, got)
}

func TestOPADecider_AllowedTools_EmptyInput_DoesNotCallOPA(t *testing.T) {
	s := newOPAStub(t)
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	got, err := d.AllowedTools(t.Context(), testPrincipal(), nil)
	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, int32(0), s.calls.Load())
}

func TestOPADecider_AllowedTools_ResultMissing_Error(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.AllowedTools(
		t.Context(),
		testPrincipal(),
		[]ToolRef{{Server: "billing-svc", Name: "create_invoice"}},
	)
	require.Error(t, err)
}

func TestOPADecider_AllowedTools_ResultNonArray_Error(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": "not-an-array"}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.AllowedTools(
		t.Context(),
		testPrincipal(),
		[]ToolRef{{Server: "billing-svc", Name: "create_invoice"}},
	)
	require.Error(t, err)
}

func TestOPADecider_AllowedTools_NonOKStatus_Error(t *testing.T) {
	s := newOPAStub(t)
	s.statusCode = http.StatusServiceUnavailable
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.AllowedTools(
		t.Context(),
		testPrincipal(),
		[]ToolRef{{Server: "billing-svc", Name: "create_invoice"}},
	)
	require.Error(t, err)
}

// --- AllowCatalog: request shape ---

func TestOPADecider_AllowCatalog_PostsToCatalogDecisionPath(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": true}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.AllowCatalog(t.Context(), testPrincipal())
	require.NoError(t, err)

	req := s.lastRequest.Load()
	require.NotNil(t, req)
	require.Equal(t, "/v1/data/acme/authz/allow_catalog", req.path)
	require.Equal(t, "application/json", req.contentType)

	input, ok := req.body["input"].(map[string]any)
	require.True(t, ok, "body must have an 'input' object")
	require.Equal(t, "user-042", input["user"])
	require.Equal(t, []any{"team-finance", "team-ops"}, input["groups"])
}

func TestOPADecider_AllowCatalog_CustomInputKeys_PostsWithConfiguredNames(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": true}`
	cfg := testAuthzConfig(s.srv.URL)
	cfg.Input = config.AuthzInput{User: "acct", Groups: "roles"}
	d := NewOPADecider(cfg, nil)

	_, err := d.AllowCatalog(t.Context(), testPrincipal())
	require.NoError(t, err)

	req := s.lastRequest.Load()
	input, ok := req.body["input"].(map[string]any)
	require.True(t, ok, "body must have an 'input' object")
	require.Equal(t, "user-042", input["acct"])
	require.Equal(t, []any{"team-finance", "team-ops"}, input["roles"])
	require.NotContains(t, input, "user")
	require.NotContains(t, input, "groups")
}

// --- AllowCatalog: result handling ---

func TestOPADecider_AllowCatalog_ResultTrue(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": true}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	allowed, err := d.AllowCatalog(t.Context(), testPrincipal())
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestOPADecider_AllowCatalog_ResultFalse(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{"result": false}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	allowed, err := d.AllowCatalog(t.Context(), testPrincipal())
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestOPADecider_AllowCatalog_ResultMissing_Error(t *testing.T) {
	s := newOPAStub(t)
	s.response = `{}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.AllowCatalog(t.Context(), testPrincipal())
	require.Error(t, err)
}

func TestOPADecider_AllowCatalog_NonOKStatus_Error(t *testing.T) {
	s := newOPAStub(t)
	s.statusCode = http.StatusInternalServerError
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.AllowCatalog(t.Context(), testPrincipal())
	require.Error(t, err)
}

func TestOPADecider_AllowCatalog_ConnectionFailure_Error(t *testing.T) {
	s := newOPAStub(t)
	s.srv.Close()
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.AllowCatalog(t.Context(), testPrincipal())
	require.Error(t, err)
}
