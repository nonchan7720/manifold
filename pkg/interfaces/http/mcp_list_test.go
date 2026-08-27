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
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, config.AuthzConfig{})
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
	h := NewMCPHandler(testMCPListServers(), catalog, config.AuthzConfig{})
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
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, config.AuthzConfig{})
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
	h := NewMCPHandler(testMCPListServers(), catalog, config.AuthzConfig{})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	entries := decodeMCPList(t, rec)
	backend := findMCPListEntry(t, entries, "backend")
	require.Nil(t, backend.Tools)
	require.Contains(t, backend.Error, "connect: dial tcp: refused")
}

func TestMCPList_ToolsQuery_AuthzEnabled_AdminGroup_ReturnsTools(t *testing.T) {
	authzCfg := config.AuthzConfig{
		Enabled:     true,
		AdminGroups: []string{"team-platform"},
		Headers:     config.AuthzHeaders{UserID: "x-user-id", UserGroups: "x-user-groups"},
	}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	req.Header.Set("x-user-id", "admin-1")
	req.Header.Set("x-user-groups", "team-platform")
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestMCPList_ToolsQuery_AuthzEnabled_NonAdminGroup_Returns403(t *testing.T) {
	authzCfg := config.AuthzConfig{
		Enabled:     true,
		AdminGroups: []string{"team-platform"},
		Headers:     config.AuthzHeaders{UserID: "x-user-id", UserGroups: "x-user-groups"},
	}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	req.Header.Set("x-user-id", "user-1")
	req.Header.Set("x-user-groups", "team-billing")
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	require.Equal(t, "forbidden", body["error"])
}

func TestMCPList_ToolsQuery_AuthzEnabled_MissingIdentityHeaders_Returns403(t *testing.T) {
	authzCfg := config.AuthzConfig{
		Enabled:     true,
		AdminGroups: []string{"team-platform"},
		Headers:     config.AuthzHeaders{UserID: "x-user-id", UserGroups: "x-user-groups"},
	}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestMCPList_ToolsQuery_AuthzEnabled_EmptyAdminGroups_Returns403(t *testing.T) {
	authzCfg := config.AuthzConfig{
		Enabled: true,
		Headers: config.AuthzHeaders{UserID: "x-user-id", UserGroups: "x-user-groups"},
	}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list?tools=true", nil)
	req.Header.Set("x-user-id", "admin-1")
	req.Header.Set("x-user-groups", "team-platform")
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestMCPList_WithoutToolsQuery_AuthzEnabled_NoIdentityRequired(t *testing.T) {
	authzCfg := config.AuthzConfig{Enabled: true, AdminGroups: []string{"team-platform"}}
	h := NewMCPHandler(testMCPListServers(), &fakeToolCatalog{}, authzCfg)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/mcp/list", nil)
	rec := httptest.NewRecorder()

	h.MCPList(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
