package httphandler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/require"
)

// fakeToolCatalog is a ToolCataloger test double keyed by server name.
type fakeToolCatalog struct {
	infos map[string][]mcpsrv.ToolInfo
	errs  map[string]error
}

func (f *fakeToolCatalog) ToolCatalog(_ context.Context, name string) ([]mcpsrv.ToolInfo, error) {
	if err, ok := f.errs[name]; ok {
		return nil, err
	}
	return f.infos[name], nil
}

// fakeCatalogDecider is an authz.Decider test double whose AllowCatalog
// result/error is configurable. Allow / AllowedTools are not exercised by
// MCPHandler and just return zero values.
type fakeCatalogDecider struct {
	allowCatalogResult bool
	allowCatalogErr    error
	calls              int
	lastPrincipal      authz.Principal
}

func (d *fakeCatalogDecider) Allow(
	context.Context, authz.Principal, authz.ToolRef,
) (bool, error) {
	return false, nil
}

func (d *fakeCatalogDecider) AllowedTools(
	context.Context, authz.Principal, []authz.ToolRef,
) ([]authz.ToolRef, error) {
	return nil, nil
}

func (d *fakeCatalogDecider) AllowCatalog(_ context.Context, p authz.Principal) (bool, error) {
	d.calls++
	d.lastPrincipal = p
	return d.allowCatalogResult, d.allowCatalogErr
}

func testMCPListServers() config.Servers {
	return config.Servers{
		"petstore": {Name: "petstore", Description: "Swagger Petstore sample API"},
		"reverse": {
			Name:        "reverse",
			Description: "browser app",
			Transport:   config.MCPTransportReverse,
			Origin:      "https://app.example.com",
		},
		"backend": {
			Name:        "backend",
			Description: "mcp backend",
			Transport:   config.MCPTransportHTTP,
			URL:         "http://backend.example.com/mcp",
		},
	}
}

// mcpListEntry decodes one /mcp/list entry, keeping Tools as json.RawMessage
// so tests can assert whether the "tools" key was present at all (nil) vs.
// present with an empty array — a distinction plain struct decoding loses.
type mcpListEntry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Tools       json.RawMessage `json:"tools"`
	Dynamic     bool            `json:"dynamic"`
	Error       string          `json:"error"`
}

func decodeMCPList(t *testing.T, rec *httptest.ResponseRecorder) []mcpListEntry {
	t.Helper()
	var body struct {
		MCP []mcpListEntry `json:"mcp"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	return body.MCP
}

func findMCPListEntry(t *testing.T, entries []mcpListEntry, name string) mcpListEntry {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no entry named %q", name)
	return mcpListEntry{}
}

func TestMCPList_WithoutToolsQuery_OmitsToolsField(t *testing.T) {
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, config.AuthzConfig{}, nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list", nil)
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	entries := decodeMCPList(t, rec)
	require.Len(t, entries, 3)
	for _, e := range entries {
		require.Nil(t, e.Tools)
		require.False(t, e.Dynamic)
	}
}

func TestMCPList_ToolsQuery_AuthzDisabled_ReturnsToolsPerServer(t *testing.T) {
	catalog := &fakeToolCatalog{
		infos: map[string][]mcpsrv.ToolInfo{
			"petstore": {{Name: "getpetbyid", Description: "Find pet by ID."}},
			"backend":  {},
		},
	}
	h := NewMCPHandler(testMCPListServers(), catalog, config.AuthzConfig{}, nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	entries := decodeMCPList(t, rec)

	petstore := findMCPListEntry(t, entries, "petstore")
	require.JSONEq(
		t,
		`[{"name":"getpetbyid","description":"Find pet by ID."}]`,
		string(petstore.Tools),
	)
	require.False(t, petstore.Dynamic)
	require.Empty(t, petstore.Error)

	backend := findMCPListEntry(t, entries, "backend")
	require.JSONEq(t, `[]`, string(backend.Tools))
}

func TestMCPList_ToolsQuery_ReverseBackend_MarksDynamicWithoutTools(t *testing.T) {
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, config.AuthzConfig{}, nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	entries := decodeMCPList(t, rec)
	reverse := findMCPListEntry(t, entries, "reverse")
	require.Nil(t, reverse.Tools)
	require.True(t, reverse.Dynamic)
}

func TestMCPList_ToolsQuery_CatalogError_ReturnsErrorAndOmitsTools_Status200(t *testing.T) {
	catalog := &fakeToolCatalog{
		errs: map[string]error{"backend": errors.New("connect: dial tcp: refused")},
	}
	h := NewMCPHandler(testMCPListServers(), catalog, config.AuthzConfig{}, nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	entries := decodeMCPList(t, rec)
	backend := findMCPListEntry(t, entries, "backend")
	require.Nil(t, backend.Tools)
	require.Contains(t, backend.Error, "connect: dial tcp: refused")
}

// --- authz enabled: catalog access delegated to the Decider ---

func testMCPListAuthzHeaders() config.AuthzHeaders {
	return config.AuthzHeaders{UserID: "x-user-id", UserGroups: "x-user-groups"}
}

func TestMCPList_ToolsQuery_AuthzEnabled_DeciderAllows_ReturnsTools(t *testing.T) {
	catalog := &fakeToolCatalog{
		infos: map[string][]mcpsrv.ToolInfo{
			"petstore": {{Name: "getpetbyid", Description: "Find pet by ID."}},
		},
	}
	authzCfg := config.AuthzConfig{Enabled: true, Headers: testMCPListAuthzHeaders()}
	decider := &fakeCatalogDecider{allowCatalogResult: true}
	h := NewMCPHandler(testMCPListServers(), catalog, authzCfg, decider)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	req.Header.Set("x-user-id", "user-1")
	req.Header.Set("x-user-groups", "team-platform")
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	entries := decodeMCPList(t, rec)
	petstore := findMCPListEntry(t, entries, "petstore")
	require.JSONEq(
		t,
		`[{"name":"getpetbyid","description":"Find pet by ID."}]`,
		string(petstore.Tools),
	)
	require.Equal(t, 1, decider.calls)
}

func TestMCPList_ToolsQuery_AuthzEnabled_DeciderDenies_Returns403(t *testing.T) {
	authzCfg := config.AuthzConfig{Enabled: true, Headers: testMCPListAuthzHeaders()}
	decider := &fakeCatalogDecider{allowCatalogResult: false}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg, decider)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	req.Header.Set("x-user-id", "user-1")
	req.Header.Set("x-user-groups", "team-billing")
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	require.Equal(t, "forbidden", body["error"])
	require.Equal(t, 1, decider.calls)
}

func TestMCPList_ToolsQuery_AuthzEnabled_DeciderErrors_Returns403(t *testing.T) {
	authzCfg := config.AuthzConfig{Enabled: true, Headers: testMCPListAuthzHeaders()}
	decider := &fakeCatalogDecider{allowCatalogErr: errors.New("opa: connection refused")}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg, decider)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	req.Header.Set("x-user-id", "user-1")
	req.Header.Set("x-user-groups", "team-platform")
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, 1, decider.calls)
}

func TestMCPList_ToolsQuery_AuthzEnabled_MissingIdentityHeaders_Returns403WithoutCallingDecider(
	t *testing.T,
) {
	authzCfg := config.AuthzConfig{Enabled: true, Headers: testMCPListAuthzHeaders()}
	decider := &fakeCatalogDecider{allowCatalogResult: true}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg, decider)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Zero(t, decider.calls)
}

func TestMCPList_WithoutToolsQuery_AuthzEnabled_NoIdentityRequired(t *testing.T) {
	authzCfg := config.AuthzConfig{Enabled: true, Headers: testMCPListAuthzHeaders()}
	decider := &fakeCatalogDecider{allowCatalogResult: false}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg, decider)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list", nil)
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Zero(t, decider.calls)
}

// --- bypass ---

func authzCfgWithBypass() config.AuthzConfig {
	return config.AuthzConfig{
		Enabled: true,
		Headers: config.AuthzHeaders{
			UserID:     "x-user-id",
			UserGroups: "x-user-groups",
			Bypass:     "x-authz-bypass",
		},
	}
}

func TestMCPList_ToolsQuery_AuthzEnabled_BypassHeaderTrue_ReturnsToolsWithoutCallingDecider(
	t *testing.T,
) {
	catalog := &fakeToolCatalog{
		infos: map[string][]mcpsrv.ToolInfo{
			"petstore": {{Name: "getpetbyid", Description: "Find pet by ID."}},
		},
	}
	decider := &fakeCatalogDecider{allowCatalogResult: false}
	h := NewMCPHandler(testMCPListServers(), catalog, authzCfgWithBypass(), decider)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	req.Header.Set("x-authz-bypass", "true")
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	entries := decodeMCPList(t, rec)
	petstore := findMCPListEntry(t, entries, "petstore")
	require.JSONEq(
		t,
		`[{"name":"getpetbyid","description":"Find pet by ID."}]`,
		string(petstore.Tools),
	)
	require.Zero(t, decider.calls)
}

// --- fromHeaders ---

func testMCPListAuthzCfgWithFromHeaders(
	fromHeaders map[string]config.AuthzInputHeaderField,
) config.AuthzConfig {
	return config.AuthzConfig{
		Enabled: true,
		Headers: testMCPListAuthzHeaders(),
		Input:   config.AuthzInput{FromHeaders: fromHeaders},
	}
}

func newMCPListIdentityRequest(t *testing.T) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	req.Header.Set("x-user-id", "user-1")
	req.Header.Set("x-user-groups", "team-platform")
	return req
}

func TestMCPList_ToolsQuery_AuthzEnabled_FromHeadersMissing_Returns403WithoutCallingDecider(
	t *testing.T,
) {
	authzCfg := testMCPListAuthzCfgWithFromHeaders(map[string]config.AuthzInputHeaderField{
		"tenant": {Header: "x-tenant-id"},
	})
	decider := &fakeCatalogDecider{allowCatalogResult: true}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg, decider)
	rec := httptest.NewRecorder()

	h.MCPList(rec, newMCPListIdentityRequest(t))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Zero(t, decider.calls)
}

func TestMCPList_ToolsQuery_AuthzEnabled_OptionalFromHeadersMissing_ReachesDecider(
	t *testing.T,
) {
	required := false
	authzCfg := testMCPListAuthzCfgWithFromHeaders(map[string]config.AuthzInputHeaderField{
		"tenant": {Header: "x-tenant-id", Required: &required},
	})
	decider := &fakeCatalogDecider{allowCatalogResult: true}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg, decider)
	rec := httptest.NewRecorder()

	h.MCPList(rec, newMCPListIdentityRequest(t))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, decider.calls)
	require.NotContains(t, decider.lastPrincipal.Extra, "tenant")
}

func TestMCPList_ToolsQuery_AuthzEnabled_FromHeadersPresent_ForwardedToDecider(t *testing.T) {
	authzCfg := testMCPListAuthzCfgWithFromHeaders(map[string]config.AuthzInputHeaderField{
		"tenant": {Header: "x-tenant-id"},
	})
	decider := &fakeCatalogDecider{allowCatalogResult: true}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg, decider)
	req := newMCPListIdentityRequest(t)
	req.Header.Set("x-tenant-id", "acme")
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, map[string]any{"tenant": "acme"}, decider.lastPrincipal.Extra)
}

func TestMCPList_ToolsQuery_AuthzEnabled_BypassHeaderNotExactlyTrue_StillRequiresDeciderAllow(
	t *testing.T,
) {
	decider := &fakeCatalogDecider{allowCatalogResult: false}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfgWithBypass(), decider)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	req.Header.Set("x-user-id", "user-1")
	req.Header.Set("x-user-groups", "team-billing")
	req.Header.Set("x-authz-bypass", "True")
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, 1, decider.calls)
}
