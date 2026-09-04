package oastomcptool

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

const inlineOpenAPI3Spec = `{
  "openapi": "3.0.0",
  "info": {"title": "Inline", "version": "1.0.0"},
  "paths": {
    "/ping": {
      "get": {
        "operationId": "ping",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

const inlineSwagger2Spec = `{
  "swagger": "2.0",
  "info": {"title": "Inline", "version": "1.0.0"},
  "paths": {
    "/ping": {
      "get": {
        "operationId": "ping",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

func TestLoadSpecSource_OpenAPI3_File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(path, []byte(inlineOpenAPI3Spec), 0o600))

	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)
	require.Equal(t, SpecFormatOpenAPI3, source.Format)
	require.NotNil(t, source.OpenAPI)
	require.Nil(t, source.Swagger)
	require.Equal(t, path, source.SpecPath)
	require.NotEmpty(t, source.Hash)
}

func TestLoadSpecSource_Swagger2_File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swagger.json")
	require.NoError(t, os.WriteFile(path, []byte(inlineSwagger2Spec), 0o600))

	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)
	require.Equal(t, SpecFormatSwagger2, source.Format)
	require.NotNil(t, source.Swagger)
	require.Nil(t, source.OpenAPI)
	require.Equal(t, path, source.SpecPath)
	require.NotEmpty(t, source.Hash)
}

func TestLoadSpecSource_OpenAPI3_URL(t *testing.T) {
	t.Setenv("TEST", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(inlineOpenAPI3Spec)) //nolint: errcheck
	}))
	defer srv.Close()

	source, err := LoadSpecSource(context.Background(), srv.URL+"/openapi.json")
	require.NoError(t, err)
	require.Equal(t, SpecFormatOpenAPI3, source.Format)
	require.NotNil(t, source.OpenAPI)
}

func TestLoadSpecSource_Swagger2_URL(t *testing.T) {
	t.Setenv("TEST", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(inlineSwagger2Spec)) //nolint: errcheck
	}))
	defer srv.Close()

	source, err := LoadSpecSource(context.Background(), srv.URL+"/swagger.json")
	require.NoError(t, err)
	require.Equal(t, SpecFormatSwagger2, source.Format)
	require.NotNil(t, source.Swagger)
}

func TestLoadSpecSource_Hash_MatchesRawBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(path, []byte(inlineOpenAPI3Spec), 0o600))

	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(raw)), source.Hash)
}

func TestLoadSpecSource_Error_InvalidPath(t *testing.T) {
	_, err := LoadSpecSource(context.Background(), "nonexistent_spec.json")
	require.Error(t, err)
}

// --- YAML detection (Finding 2) ---

// inlineSwagger2YAMLSpec uses an unquoted "200:" response key, which is
// valid YAML but not valid JSON (a bare number as a map key) — the case
// json.Unmarshal-only detection used to miss.
const inlineSwagger2YAMLSpec = `
swagger: "2.0"
info:
  title: Inline
  version: "1.0.0"
paths:
  /ping:
    get:
      operationId: ping
      responses:
        200:
          description: ok
`

const inlineOpenAPI3YAMLSpec = `
openapi: 3.0.0
info:
  title: Inline
  version: 1.0.0
paths:
  /ping:
    get:
      operationId: ping
      responses:
        "200":
          description: ok
`

func TestLoadSpecSource_Swagger2_YAML_File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swagger.yaml")
	require.NoError(t, os.WriteFile(path, []byte(inlineSwagger2YAMLSpec), 0o600))

	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)
	require.Equal(t, SpecFormatSwagger2, source.Format)
	require.NotNil(t, source.Swagger)
	require.Nil(t, source.OpenAPI)
	pathItem, ok := source.Swagger.Paths["/ping"]
	require.True(t, ok)
	require.NotNil(t, pathItem.Get)
	require.Equal(t, "ping", pathItem.Get.OperationID)
}

func TestLoadSpecSource_OpenAPI3_YAML_File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	require.NoError(t, os.WriteFile(path, []byte(inlineOpenAPI3YAMLSpec), 0o600))

	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)
	require.Equal(t, SpecFormatOpenAPI3, source.Format)
	require.NotNil(t, source.OpenAPI)
	require.Nil(t, source.Swagger)
}

// --- Single fetch (Finding 3) ---

// TestLoadSpecSource_SingleFetch_HashMatchesFirstResponse serves a
// different document on each request (operation "a" then "b") and checks
// that LoadSpecSource's Hash matches the FIRST response and its parsed spec
// still contains operation "a" — i.e. exactly one fetch is shared between
// hashing and parsing, so the two can never describe different documents.
func TestLoadSpecSource_SingleFetch_HashMatchesFirstResponse(t *testing.T) {
	t.Setenv("TEST", "true")

	const specTmpl = `{
  "openapi": "3.0.0",
  "info": {"title": "Inline", "version": "1.0.0"},
  "paths": {
    "/%s": {
      "get": {
        "operationId": "%s",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`
	firstResp := fmt.Sprintf(specTmpl, "a", "opA")
	secondResp := fmt.Sprintf(specTmpl, "b", "opB")

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			w.Write([]byte(firstResp)) //nolint: errcheck
			return
		}
		w.Write([]byte(secondResp)) //nolint: errcheck
	}))
	defer srv.Close()

	source, err := LoadSpecSource(context.Background(), srv.URL+"/openapi.json")
	require.NoError(t, err)

	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte(firstResp))), source.Hash)
	require.Equal(t, int32(1), requests.Load(), "LoadSpecSource must fetch the spec exactly once")

	require.NotNil(t, source.OpenAPI)
	_, hasA := source.OpenAPI.Paths.Map()["/a"]
	require.True(t, hasA, "parsed spec must be the first response (operation on /a)")
	_, hasB := source.OpenAPI.Paths.Map()["/b"]
	require.False(t, hasB, "parsed spec must not be the second response (operation on /b)")
}

// TestLoadSpecSource_OpenAPI3_RelativeExternalRef_File checks that a spec's
// relative external $ref to a sibling file still resolves when loaded from
// a local path, now that LoadSpecSource parses the already-fetched bytes
// (LoadOpenAPI3SpecFromData) instead of re-reading specPath from disk.
func TestLoadSpecSource_OpenAPI3_RelativeExternalRef_File(t *testing.T) {
	dir := t.TempDir()
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
	const mainDoc = `{
  "openapi": "3.0.0",
  "info": {"title": "Inline", "version": "1.0.0"},
  "paths": {
    "/widgets": {
      "get": {
        "operationId": "getWidgets",
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {"$ref": "schemas.json#/components/schemas/Widget"}
              }
            }
          }
        }
      }
    }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schemas.json"), []byte(schemasDoc), 0o600))
	mainPath := filepath.Join(dir, "openapi.json")
	require.NoError(t, os.WriteFile(mainPath, []byte(mainDoc), 0o600))

	source, err := LoadSpecSource(context.Background(), mainPath)
	require.NoError(t, err)
	require.Equal(t, SpecFormatOpenAPI3, source.Format)

	schema := source.OpenAPI.Paths.Find("/widgets").Get.Responses.
		Value("200").Value.Content.Get("application/json").Schema
	require.NotNil(t, schema.Value, "relative external $ref must resolve to the sibling file")
	require.Contains(t, schema.Value.Properties, "name")
}

// TestLoadSpecSource_OpenAPI3_RelativeExternalRef_URL is the URL-served
// counterpart of the above: both the main spec and the sibling it
// references come from httptest, exercising the http(s) branch of
// openAPI3SpecLocation.
func TestLoadSpecSource_OpenAPI3_RelativeExternalRef_URL(t *testing.T) {
	t.Setenv("TEST", "true")

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
	const mainDoc = `{
  "openapi": "3.0.0",
  "info": {"title": "Inline", "version": "1.0.0"},
  "paths": {
    "/widgets": {
      "get": {
        "operationId": "getWidgets",
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {"$ref": "schemas.json#/components/schemas/Widget"}
              }
            }
          }
        }
      }
    }
  }
}`
	mux := http.NewServeMux()
	mux.HandleFunc("/schemas.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(schemasDoc)) //nolint: errcheck
	})
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(mainDoc)) //nolint: errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	source, err := LoadSpecSource(context.Background(), srv.URL+"/openapi.json")
	require.NoError(t, err)

	schema := source.OpenAPI.Paths.Find("/widgets").Get.Responses.
		Value("200").Value.Content.Get("application/json").Schema
	require.NotNil(t, schema.Value, "relative external $ref must resolve via the sibling URL")
	require.Contains(t, schema.Value.Properties, "name")
}
