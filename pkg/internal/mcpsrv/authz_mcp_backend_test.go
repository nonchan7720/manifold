package mcpsrv

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/require"
)

// This file exercises the real end-to-end wiring described in
// examples/opa/: an OPA sidecar authorizing tools/call and tools/list on an
// MCP backend server (as opposed to authz_middleware_test.go, which builds
// the middleware directly on a hand-rolled *mcp.Server). It proves that the
// same policy shape works for an MCP backend, not just OpenAPI mode, and
// that the backend's mcpServers key ("backend") is what reaches OPA as
// input.server.

// authzGroupPatterns is a tiny in-test stand-in for examples/opa/data.json,
// scoped to this test's own server/tool names.
var authzGroupPatterns = map[string][]string{
	"readers":   {"backend/echo"},
	"operators": {"backend/*"},
}

// authzGroupsAllow mirrors examples/opa/policy.rego's glob.match semantics
// (a trailing "*" matches any suffix, otherwise an exact match is required).
func authzGroupsAllow(groups []string, server, tool string) bool {
	target := server + "/" + tool
	for _, g := range groups {
		for _, pattern := range authzGroupPatterns[g] {
			if strings.HasSuffix(pattern, "*") {
				if strings.HasPrefix(target, strings.TrimSuffix(pattern, "*")) {
					return true
				}
			} else if pattern == target {
				return true
			}
		}
	}
	return false
}

// opaCallInput records the input authz.OPADecider posted for the most
// recent tools/call decision (decisionPath.call), so a test can assert what
// server/tool the fake OPA actually saw.
type opaCallInput struct {
	Server string
	Tool   string
}

// fakeOPAServer implements just the two REST data API paths
// authz.NewOPADecider queries by default (see pkg/config/authz.go's
// DefaultAuthzDecisionPathCall / DefaultAuthzDecisionPathList), deciding
// with authzGroupsAllow instead of a real Rego evaluation.
type fakeOPAServer struct {
	srv      *httptest.Server
	calls    atomic.Int32
	lastCall atomic.Pointer[opaCallInput]
}

// newFakeOPAServer starts the fake OPA on an httptest server that is closed
// when t finishes.
func newFakeOPAServer(t *testing.T) *fakeOPAServer {
	t.Helper()
	f := &fakeOPAServer{}
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/data/mcp/authz/allow", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		var body struct {
			Input struct {
				Groups []string `json:"groups"`
				Server string   `json:"server"`
				Tool   string   `json:"tool"`
			} `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		f.lastCall.Store(&opaCallInput{Server: body.Input.Server, Tool: body.Input.Tool})

		allowed := authzGroupsAllow(body.Input.Groups, body.Input.Server, body.Input.Tool)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": allowed})
	})

	allowedTools := func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		var body struct {
			Input struct {
				Groups []string `json:"groups"`
				Tools  []struct {
					Server string `json:"server"`
					Name   string `json:"name"`
				} `json:"tools"`
			} `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)

		result := []map[string]string{}
		for _, tool := range body.Input.Tools {
			if authzGroupsAllow(body.Input.Groups, tool.Server, tool.Name) {
				result = append(result, map[string]string{"server": tool.Server, "name": tool.Name})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}
	mux.HandleFunc("/v1/data/mcp/authz/allowed_tools", allowedTools)

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// callCount reports how many decision requests the fake OPA has received.
func (f *fakeOPAServer) callCount() int32 { return f.calls.Load() }

// newAuthzMCPBackendServer returns an httptest-served MCP backend exposing
// "echo" (safe) and "secret" (sensitive) tools, over Streamable HTTP.
func newAuthzMCPBackendServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{
			Name:        "echo",
			Description: "echo the input",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "echoed"}},
			}, nil
		},
	)
	srv.AddTool(
		&mcp.Tool{
			Name:        "secret",
			Description: "reveal a secret",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "top-secret"}},
			}, nil
		},
	)
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	httpSrv := httptest.NewServer(handler)
	t.Cleanup(httpSrv.Close)
	return httpSrv
}

// toolNames extracts tool.Name from tools, preserving order.
func toolNames(tools []*mcp.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

// TestAuthzMCPBackend_EndToEnd drives tools/list and tools/call through the
// gateway's MCP backend server with the OPA-backed authz middleware wired in
// exactly as pkg/cmd/server.go does, asserting allow, deny, list filtering,
// and fail-closed behaviour without identity headers.
func TestAuthzMCPBackend_EndToEnd(t *testing.T) {
	// The shared internal transport (used for both the MCP backend
	// connection and the OPADecider's OPA client) only allows dialing
	// loopback addresses when TEST is set; see server_manager_test.go's
	// newToolCatalogBackendServer.
	t.Setenv("TEST", "true")

	backend := newAuthzMCPBackendServer(t)
	fakeOPA := newFakeOPAServer(t)

	cfg := config.AuthzConfig{
		Enabled: true,
		OPAURL:  fakeOPA.srv.URL,
	}.WithDefaults()
	decider := authz.NewOPADecider(cfg, nil)

	servers := config.Servers{
		"backend": &config.Server{
			Name:      "backend",
			Transport: config.MCPTransportHTTP,
			URL:       backend.URL,
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(
		servers,
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
		WithServerMiddleware(func(name string) []mcp.Middleware {
			return []mcp.Middleware{
				NewAuthzMiddleware(name, decider, cfg.Headers, cfg.Input.FromHeaders),
			}
		}),
	)
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)

	gwSrv, err := s.Server("backend")
	require.NoError(t, err)

	// Served over real Streamable HTTP (not mcp.NewInMemoryTransports) so
	// the request carries real HTTP headers — req.GetExtra().Header is only
	// populated by an HTTP-backed transport (see the comment near line 401
	// of authz_middleware_test.go).
	gwHTTPSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return gwSrv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(gwHTTPSrv.Close)

	connect := func(headers http.Header) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
		session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
			Endpoint:   gwHTTPSrv.URL,
			HTTPClient: &http.Client{Transport: &headerRoundTripper{headers: headers}},
		}, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = session.Close() })
		return session
	}

	readerHeaders := http.Header{}
	readerHeaders.Set("x-user-id", "user-001")
	readerHeaders.Set("x-user-groups", "readers")

	// --- readers: tools/list only shows echo ---
	readerSession := connect(readerHeaders)
	listResult, err := readerSession.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"echo"}, toolNames(listResult.Tools))

	// --- readers: tools/call echo succeeds and returns the backend's content ---
	echoResult, err := readerSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "echo"})
	require.NoError(t, err)
	require.False(t, echoResult.IsError)
	echoText, ok := echoResult.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "echoed", echoText.Text)

	// --- readers: tools/call secret is denied ---
	_, err = readerSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "secret"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")

	// The fake OPA saw the MCP backend's mcpServers key as input.server, and
	// the denied tool name as input.tool.
	last := fakeOPA.lastCall.Load()
	require.NotNil(t, last)
	require.Equal(t, "backend", last.Server)
	require.Equal(t, "secret", last.Tool)

	// --- operators: tools/list shows both tools; tools/call secret succeeds ---
	operatorHeaders := http.Header{}
	operatorHeaders.Set("x-user-id", "user-002")
	operatorHeaders.Set("x-user-groups", "operators")
	operatorSession := connect(operatorHeaders)

	opList, err := operatorSession.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"echo", "secret"}, toolNames(opList.Tools))

	secretResult, err := operatorSession.CallTool(
		t.Context(), &mcp.CallToolParams{Name: "secret"},
	)
	require.NoError(t, err)
	require.False(t, secretResult.IsError)
	secretText, ok := secretResult.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "top-secret", secretText.Text)

	// --- no identity headers: tools/list is denied without ever reaching OPA ---
	callsBefore := fakeOPA.callCount()
	anonSession := connect(http.Header{})
	_, err = anonSession.ListTools(t.Context(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Equal(t, callsBefore, fakeOPA.callCount())
}
