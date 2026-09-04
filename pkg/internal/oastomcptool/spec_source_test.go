package oastomcptool

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
