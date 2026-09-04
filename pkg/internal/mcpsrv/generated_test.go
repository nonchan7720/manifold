package mcpsrv

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/stretchr/testify/require"
)

// goldenSpec is a small (2-operation) OpenAPI 3 spec used to pin the exact
// YAML text a generated tools file gets, so a future yaml.v3 upgrade or an
// accidental field-order change shows up as a test failure (see the design
// memo's 判断事項 8).
const goldenSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Widget API", "version": "1.0.0"},
  "paths": {
    "/widgets/{id}": {
      "get": {
        "operationId": "getWidget",
        "summary": "Get a widget",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/widgets": {
      "post": {
        "operationId": "createWidget",
        "summary": "Create a widget",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {"name": {"type": "string"}},
                "required": ["name"]
              }
            }
          }
        },
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

func newGeneratedCatalogFromSpec(t *testing.T, path string) *oastomcptool.GeneratedCatalog {
	t.Helper()
	source, err := oastomcptool.LoadSpecSource(t.Context(), path)
	require.NoError(t, err)

	registry, err := BuildCatalog(
		t.Context(), &http.Client{}, source, "https://api.example.com", nil,
	)
	require.NoError(t, err)

	fetchedAt := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	g, err := oastomcptool.NewGeneratedCatalog(
		t.Context(), source, GeneratedTools(registry.Definitions()), "manifold test", fetchedAt,
	)
	require.NoError(t, err)
	return g
}

func TestGeneratedCatalog_Golden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(path, []byte(goldenSpec), 0o600))

	g := newGeneratedCatalogFromSpec(t, path)
	// source.spec 以外は決定的な入力なので、ここだけ差し替えて golden と比較する。
	g.Source.Spec = path

	var buf bytes.Buffer
	require.NoError(t, oastomcptool.WriteGeneratedCatalog(&buf, g))

	want := goldenYAML(path, fmt.Sprintf("%x", sha256.Sum256([]byte(goldenSpec))))
	require.Equal(t, want, buf.String())
}

// goldenYAML pins the exact YAML text produced for goldenSpec: field order
// (version, generatedBy, source{spec,sha256,fetchedAt}, format, tools,
// spec), 2-space indent, and sorted map keys under "spec". Any drift here
// (a yaml.v3 upgrade, a struct field reorder) should show up as a diff.
func goldenYAML(path, sha256Hex string) string {
	return fmt.Sprintf(`version: 1
generatedBy: manifold test
source:
  spec: %s
  sha256: %s
  fetchedAt: "2026-09-04T00:00:00Z"
format: openapi3
tools:
  - name: createwidget
    operation: POST /widgets
    description: Create a widget
    binaryResponse: false
    inputSchema:
      properties:
        body:
          description: 'Request body. JSON object with fields: {name (string, required)}'
          properties:
            name:
              _meta: {}
              description: ""
              type: string
          required:
            - name
          type: object
      required:
        - body
      type: object
  - name: getwidget
    operation: GET /widgets/{id}
    description: Get a widget
    binaryResponse: false
    inputSchema:
      properties:
        id:
          description: ""
          type: string
      required:
        - id
      type: object
spec:
  info:
    title: Widget API
    version: 1.0.0
  openapi: 3.0.0
  paths:
    /widgets:
      post:
        operationId: createWidget
        requestBody:
          content:
            application/json:
              schema:
                properties:
                  name:
                    type: string
                required:
                  - name
                type: object
          required: true
        responses:
          "200":
            description: ok
        summary: Create a widget
    /widgets/{id}:
      get:
        operationId: getWidget
        parameters:
          - in: path
            name: id
            required: true
            schema:
              type: string
        responses:
          "200":
            description: ok
        summary: Get a widget
`, path, sha256Hex)
}

// httpRefPattern matches a "$ref" YAML value pointing at an http(s) URL —
// used to assert that a generated catalog's internalized spec never keeps
// an external reference.
var httpRefPattern = regexp.MustCompile(`\$ref:\s*['"]?https?://`)

// --- External $ref internalization ---

// TestGeneratedCatalog_ExternalRefInternalization serves a main spec and a
// separate schemas document from httptest, with the main spec referencing
// the separate document by an external $ref. It builds a generated catalog
// from that live source, closes the httptest server (so nothing after this
// point can reach the network), writes the catalog to a file, and checks
// that LoadGeneratedSpecSource / BuildCatalog on that file reproduce the
// exact same tool definitions as the live build did — proving the external
// $ref was internalized rather than merely deferred.
func TestGeneratedCatalog_ExternalRefInternalization(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要

	const schemasDoc = `{
  "components": {
    "schemas": {
      "Widget": {
        "type": "object",
        "properties": {"name": {"type": "string"}}
      }
    }
  }
}`

	var mainDocFn func() string
	mux := http.NewServeMux()
	mux.HandleFunc("/schemas.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(schemasDoc))
	})
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mainDocFn()))
	})
	srv := httptest.NewServer(mux)
	mainDocFn = func() string {
		return fmt.Sprintf(`{
  "openapi": "3.0.0",
  "info": {"title": "Widget API", "version": "1.0.0"},
  "paths": {
    "/widgets/{id}": {
      "get": {
        "operationId": "getWidget",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}
        ],
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {"$ref": "%s/schemas.json#/components/schemas/Widget"}
              }
            }
          }
        }
      }
    }
  }
}`, srv.URL)
	}

	source, err := oastomcptool.LoadSpecSource(t.Context(), srv.URL+"/openapi.json")
	require.NoError(t, err)

	liveRegistry, err := BuildCatalog(t.Context(), &http.Client{}, source, srv.URL, nil)
	require.NoError(t, err)
	liveDefs := liveRegistry.Definitions()
	require.Len(t, liveDefs, 1)

	g, err := oastomcptool.NewGeneratedCatalog(
		t.Context(), source, GeneratedTools(liveDefs), "manifold test", time.Now(),
	)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, oastomcptool.WriteGeneratedCatalog(&buf, g))
	require.False(
		t, httpRefPattern.MatchString(buf.String()),
		"generated YAML must not keep an external $ref:\n%s", buf.String(),
	)

	srv.Close() // 以降ネットワークには一切アクセスできない

	path := filepath.Join(t.TempDir(), "generated.yaml")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))

	loadedSource, catalog, err := oastomcptool.LoadGeneratedSpecSource(t.Context(), path)
	require.NoError(t, err)

	loadedRegistry, err := BuildCatalog(t.Context(), &http.Client{}, loadedSource, srv.URL, nil)
	require.NoError(t, err)
	require.Equal(t, liveDefs, loadedRegistry.Definitions())
	require.NoError(t, VerifyGeneratedTools(loadedRegistry, catalog))
}

// --- VerifyGeneratedTools ---

func verifyTestRegistry(t *testing.T) *MCPToolRegistry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(path, []byte(goldenSpec), 0o600))
	source, err := oastomcptool.LoadSpecSource(t.Context(), path)
	require.NoError(t, err)
	registry, err := BuildCatalog(
		t.Context(), &http.Client{}, source, "https://api.example.com", nil,
	)
	require.NoError(t, err)
	return registry
}

func TestVerifyGeneratedTools_Matching(t *testing.T) {
	registry := verifyTestRegistry(t)
	g := &oastomcptool.GeneratedCatalog{Tools: GeneratedTools(registry.Definitions())}
	require.NoError(t, VerifyGeneratedTools(registry, g))
}

func TestVerifyGeneratedTools_ChangedDescription(t *testing.T) {
	registry := verifyTestRegistry(t)
	tools := GeneratedTools(registry.Definitions())
	for i := range tools {
		if tools[i].Name == "getwidget" {
			tools[i].Description = "stale description"
		}
	}
	err := VerifyGeneratedTools(registry, &oastomcptool.GeneratedCatalog{Tools: tools})
	require.Error(t, err)
	require.ErrorContains(t, err, `"getwidget"`)
	require.ErrorContains(t, err, "description differs")
}

func TestVerifyGeneratedTools_ChangedInputSchema(t *testing.T) {
	registry := verifyTestRegistry(t)
	tools := GeneratedTools(registry.Definitions())
	for i := range tools {
		if tools[i].Name == "getwidget" {
			tools[i].InputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
	}
	err := VerifyGeneratedTools(registry, &oastomcptool.GeneratedCatalog{Tools: tools})
	require.Error(t, err)
	require.ErrorContains(t, err, `"getwidget"`)
	require.ErrorContains(t, err, "inputSchema differs")
}

func TestVerifyGeneratedTools_MissingFromGeneratedFile(t *testing.T) {
	registry := verifyTestRegistry(t)
	tools := GeneratedTools(registry.Definitions())
	// "createwidget" を生成物の tools から落とし、spec 側にしか無い状態にする。
	trimmed := tools[:0]
	for _, tool := range tools {
		if tool.Name != "createwidget" {
			trimmed = append(trimmed, tool)
		}
	}
	err := VerifyGeneratedTools(registry, &oastomcptool.GeneratedCatalog{Tools: trimmed})
	require.Error(t, err)
	require.ErrorContains(t, err, `"createwidget"`)
	require.ErrorContains(t, err, "missing from generated file")
}

func TestVerifyGeneratedTools_ExtraToolNotProducedBySpec(t *testing.T) {
	registry := verifyTestRegistry(t)
	tools := GeneratedTools(registry.Definitions())
	tools = append(tools, oastomcptool.GeneratedTool{
		Name: "deletedwidget", Operation: "DELETE /widgets/{id}",
	})
	err := VerifyGeneratedTools(registry, &oastomcptool.GeneratedCatalog{Tools: tools})
	require.Error(t, err)
	require.ErrorContains(t, err, `"deletedwidget"`)
	require.ErrorContains(t, err, "not produced by spec")
}

// 実物に近い Petstore fixture で「生成 → 書き出し → tools.file から起動」を往復させ、
// 突き合わせが通ることを確認する。required の順序が map 走査に依存していた頃は
// ここが確率的に失敗していたため、複数回繰り返す。
func TestGeneratedCatalog_RoundTrip_PetstoreFixture(t *testing.T) {
	for range 5 {
		src, err := oastomcptool.LoadSpecSource(t.Context(), "fixtures/petstore_oas.json")
		require.NoError(t, err)
		reg, err := BuildCatalog(t.Context(), &http.Client{}, src, "http://backend", nil)
		require.NoError(t, err)

		g, err := oastomcptool.NewGeneratedCatalog(
			t.Context(), src, GeneratedTools(reg.Definitions()), "manifold test", time.Time{},
		)
		require.NoError(t, err)
		var buf bytes.Buffer
		require.NoError(t, oastomcptool.WriteGeneratedCatalog(&buf, g))
		path := filepath.Join(t.TempDir(), "petstore.yaml")
		require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))

		reg2, err := RegisterOpenAPI(
			t.Context(), "unused", "http://backend", nil, WithGeneratedToolsFile(path),
		)
		require.NoError(t, err)
		require.Equal(t, len(reg.Definitions()), len(reg2.Definitions()))
	}
}
