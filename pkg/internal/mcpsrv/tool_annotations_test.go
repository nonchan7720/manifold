package mcpsrv

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/stretchr/testify/require"
)

func TestParseAnnotationsExtension(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		for _, ext := range []map[string]any{
			nil,
			{"x-other": true},
			{"x-mcp": map[string]any{"description": "d"}},
		} {
			got, err := ParseAnnotationsExtension(ext)
			require.NoError(t, err)
			require.True(t, got.isZero())
		}
	})

	t.Run("invalid", func(t *testing.T) {
		for name, ext := range map[string]map[string]any{
			"x-mcp not object":       {"x-mcp": "readOnly"},
			"annotations not object": {"x-mcp": map[string]any{"annotations": "readOnly"}},
			"unknown kind":           {"x-mcp": map[string]any{"annotations": map[string]any{"kind": "safe"}}},
			"kind not string":        {"x-mcp": map[string]any{"annotations": map[string]any{"kind": 1.0}}},
			"hint not bool":          {"x-mcp": map[string]any{"annotations": map[string]any{"readOnlyHint": "true"}}},
			"title not string":       {"x-mcp": map[string]any{"annotations": map[string]any{"title": false}}},
			"unknown key":            {"x-mcp": map[string]any{"annotations": map[string]any{"readonly": true}}},
		} {
			t.Run(name, func(t *testing.T) {
				_, err := ParseAnnotationsExtension(ext)
				require.Error(t, err)
			})
		}
	})
}

func TestWithRegisterToolAnnotationsExtension(t *testing.T) {
	annotationsFor := func(t *testing.T, method string, annotations map[string]any) *mcp.ToolAnnotations {
		t.Helper()
		ext, err := ParseAnnotationsExtension(map[string]any{
			"x-mcp": map[string]any{"annotations": annotations},
		})
		require.NoError(t, err)
		r := NewMCPToolRegistry()
		r.RegisterTool("op", "desc", nil, nil,
			WithRegisterToolOperation(method, "/items"),
			WithRegisterToolAnnotationsExtension(ext),
		)
		a := findTool(t, r, "op").tool.Annotations
		require.NotNil(t, a)
		return a
	}

	t.Run("kind destructive on POST", func(t *testing.T) {
		a := annotationsFor(t, "post", map[string]any{"kind": "destructive"})
		require.False(t, a.ReadOnlyHint)
		require.True(t, *a.DestructiveHint)
	})

	t.Run("kind write on GET drops read-only presets", func(t *testing.T) {
		a := annotationsFor(t, "get", map[string]any{"kind": "write"})
		require.False(t, a.ReadOnlyHint)
		require.False(t, *a.DestructiveHint)
		require.False(t, a.IdempotentHint)
	})

	t.Run("kind readOnly on POST", func(t *testing.T) {
		a := annotationsFor(t, "post", map[string]any{"kind": "readOnly"})
		require.True(t, a.ReadOnlyHint)
		require.False(t, *a.DestructiveHint)
		require.True(t, a.IdempotentHint)
	})

	t.Run("hints override the kind", func(t *testing.T) {
		a := annotationsFor(t, "delete", map[string]any{
			"kind":            "write",
			"destructiveHint": true,
			"idempotentHint":  true,
			"openWorldHint":   false,
			"title":           "Remove item",
		})
		require.False(t, a.ReadOnlyHint)
		require.True(t, *a.DestructiveHint)
		require.True(t, a.IdempotentHint)
		require.NotNil(t, a.OpenWorldHint)
		require.False(t, *a.OpenWorldHint)
		require.Equal(t, "Remove item", a.Title)
	})

	t.Run("hints alone keep the method default", func(t *testing.T) {
		a := annotationsFor(t, "put", map[string]any{"idempotentHint": true})
		require.False(t, a.ReadOnlyHint)
		require.False(t, *a.DestructiveHint)
		require.True(t, a.IdempotentHint)
	})
}

const annotationsSpec = `{
  "openapi": "3.0.3",
  "info": {"title": "t", "version": "1"},
  "paths": {
    "/items": {
      "get": {
        "operationId": "listItems",
        "responses": {"200": {"description": "ok"}}
      },
      "post": {
        "operationId": "searchItems",
        "x-mcp": {"annotations": {"kind": "readOnly"}},
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/items/{id}": {
      "put": {
        "operationId": "replaceItem",
        "x-mcp": {"annotations": {"kind": "destructive", "idempotentHint": true}},
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

const annotationsSwaggerSpec = `{
  "swagger": "2.0",
  "info": {"title": "t", "version": "1"},
  "host": "api.example.com",
  "paths": {
    "/items": {
      "post": {
        "operationId": "searchItems",
        "x-mcp": {"annotations": {"kind": "readOnly"}},
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

func buildAnnotationsCatalog(t *testing.T, spec string) (*MCPToolRegistry, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(path, []byte(spec), 0o600))
	source, err := oastomcptool.LoadSpecSource(t.Context(), path)
	require.NoError(t, err)
	return BuildCatalog(t.Context(), &http.Client{}, source, "https://api.example.com", nil)
}

func TestBuildCatalog_AnnotationsExtension(t *testing.T) {
	t.Run("openapi3", func(t *testing.T) {
		r, err := buildAnnotationsCatalog(t, annotationsSpec)
		require.NoError(t, err)

		list := findTool(t, r, "listitems").tool.Annotations
		require.True(t, list.ReadOnlyHint)

		search := findTool(t, r, "searchitems").tool.Annotations
		require.True(t, search.ReadOnlyHint)
		require.False(t, *search.DestructiveHint)

		replace := findTool(t, r, "replaceitem").tool.Annotations
		require.False(t, replace.ReadOnlyHint)
		require.True(t, *replace.DestructiveHint)
		require.True(t, replace.IdempotentHint)
	})

	t.Run("swagger2", func(t *testing.T) {
		r, err := buildAnnotationsCatalog(t, annotationsSwaggerSpec)
		require.NoError(t, err)
		search := findTool(t, r, "searchitems").tool.Annotations
		require.True(t, search.ReadOnlyHint)
	})

	t.Run("invalid extension is ignored", func(t *testing.T) {
		r, err := buildAnnotationsCatalog(t, `{
  "openapi": "3.0.3",
  "info": {"title": "t", "version": "1"},
  "paths": {"/items": {"delete": {
    "operationId": "deleteItems",
    "x-mcp": {"annotations": {"kind": "safe", "readOnlyHint": true}},
    "responses": {"200": {"description": "ok"}}
  }}}
}`)
		require.NoError(t, err)
		a := findTool(t, r, "deleteitems").tool.Annotations
		require.False(t, a.ReadOnlyHint)
		require.True(t, *a.DestructiveHint)
	})
}
