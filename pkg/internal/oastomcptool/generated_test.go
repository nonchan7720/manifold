package oastomcptool

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGeneratedCatalog_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(path, []byte(inlineOpenAPI3Spec), 0o600))

	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)

	fetchedAt := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	tools := []GeneratedTool{
		{
			Name:           "ping",
			Operation:      "GET /ping",
			Description:    "GET /ping",
			BinaryResponse: false,
			InputSchema:    map[string]any{"type": "object"},
		},
	}
	g, err := NewGeneratedCatalog(context.Background(), source, tools, "manifold test", fetchedAt)
	require.NoError(t, err)
	require.Equal(t, GeneratedVersion, g.Version)
	require.Equal(t, SpecFormatOpenAPI3, g.Format)

	var buf bytes.Buffer
	require.NoError(t, WriteGeneratedCatalog(&buf, g))

	got, err := ReadGeneratedCatalog(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	require.Equal(t, g, got)
}

func TestGeneratedCatalog_Swagger2_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swagger.json")
	require.NoError(t, os.WriteFile(path, []byte(inlineSwagger2Spec), 0o600))
	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)

	tools := []GeneratedTool{{
		Name: "ping", Operation: "GET /ping", Description: "GET /ping",
		InputSchema: map[string]any{"type": "object"},
	}}
	g, err := NewGeneratedCatalog(context.Background(), source, tools, "manifold test", time.Now())
	require.NoError(t, err)
	require.Equal(t, SpecFormatSwagger2, g.Format)
	// The Swagger 2.x document is embedded as-is, not converted to OpenAPI 3.
	require.Equal(t, "2.0", g.Spec["swagger"])
	require.NotContains(t, g.Spec, "openapi")

	var buf bytes.Buffer
	require.NoError(t, WriteGeneratedCatalog(&buf, g))
	got, err := ReadGeneratedCatalog(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	require.Equal(t, g, got)
}

func TestNewGeneratedCatalog_RejectsUnknownFormat(t *testing.T) {
	_, err := NewGeneratedCatalog(
		context.Background(), &SpecSource{Format: "raml"}, nil, "manifold test", time.Now(),
	)
	require.Error(t, err)
	require.ErrorContains(t, err, `unsupported spec format "raml"`)
}

func TestReadGeneratedCatalog_RejectsUnknownVersion(t *testing.T) {
	g := &GeneratedCatalog{Version: 2, Format: SpecFormatOpenAPI3}
	var buf bytes.Buffer
	require.NoError(t, WriteGeneratedCatalog(&buf, g))

	_, err := ReadGeneratedCatalog(&buf)
	require.Error(t, err)
	require.ErrorContains(t, err, "version")
}

func TestReadGeneratedCatalog_RejectsUnknownFormat(t *testing.T) {
	g := &GeneratedCatalog{Version: GeneratedVersion, Format: "raml"}
	var buf bytes.Buffer
	require.NoError(t, WriteGeneratedCatalog(&buf, g))

	_, err := ReadGeneratedCatalog(&buf)
	require.Error(t, err)
	require.ErrorContains(t, err, "format")
}

// --- LoadGeneratedSpecSource ---

func TestLoadGeneratedSpecSource_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(path, []byte(inlineOpenAPI3Spec), 0o600))
	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)

	g, err := NewGeneratedCatalog(
		context.Background(), source, nil, "manifold test", time.Now(),
	)
	require.NoError(t, err)

	genPath := filepath.Join(t.TempDir(), "generated.yaml")
	f, err := os.Create(genPath)
	require.NoError(t, err)
	require.NoError(t, WriteGeneratedCatalog(f, g))
	require.NoError(t, f.Close())

	loaded, catalog, err := LoadGeneratedSpecSource(context.Background(), genPath)
	require.NoError(t, err)
	require.Equal(t, SpecFormatOpenAPI3, loaded.Format)
	require.NotNil(t, loaded.OpenAPI)
	require.Equal(t, source.SpecPath, loaded.SpecPath)
	// Hash is the sha256 of the generated FILE bytes, not of source.Hash /
	// g.Source.SHA256 (the original spec bytes) — they're expected to differ.
	require.NotEmpty(t, loaded.Hash)
	require.NotEqual(t, g.Source.SHA256, loaded.Hash)
	require.Equal(t, g.Source.Spec, catalog.Source.Spec)
}

func TestLoadGeneratedSpecSource_Swagger2_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swagger.json")
	require.NoError(t, os.WriteFile(path, []byte(inlineSwagger2Spec), 0o600))
	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)

	g, err := NewGeneratedCatalog(
		context.Background(), source, nil, "manifold test", time.Now(),
	)
	require.NoError(t, err)

	genPath := filepath.Join(t.TempDir(), "generated.yaml")
	f, err := os.Create(genPath)
	require.NoError(t, err)
	require.NoError(t, WriteGeneratedCatalog(f, g))
	require.NoError(t, f.Close())

	loaded, _, err := LoadGeneratedSpecSource(context.Background(), genPath)
	require.NoError(t, err)
	require.Equal(t, SpecFormatSwagger2, loaded.Format)
	require.Nil(t, loaded.OpenAPI)
	require.NotNil(t, loaded.Swagger)
	require.Equal(t, source.Swagger, loaded.Swagger)
	require.Equal(t, source.SpecPath, loaded.SpecPath)
}

func TestLoadGeneratedSpec_Swagger2_ConvertsToOpenAPI3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swagger.json")
	require.NoError(t, os.WriteFile(path, []byte(inlineSwagger2Spec), 0o600))
	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)
	g, err := NewGeneratedCatalog(
		context.Background(), source, nil, "manifold test", time.Now(),
	)
	require.NoError(t, err)

	doc, err := LoadGeneratedSpec(context.Background(), g)
	require.NoError(t, err)
	require.NotEmpty(t, doc.OpenAPI)
	for path := range source.Swagger.Paths {
		require.NotNil(t, doc.Paths.Find(path), "path %q should survive the conversion", path)
	}
}

func TestLoadGeneratedSpecSource_RejectsBadVersion(t *testing.T) {
	genPath := filepath.Join(t.TempDir(), "generated.yaml")
	f, err := os.Create(genPath)
	require.NoError(t, err)
	require.NoError(t, WriteGeneratedCatalog(f, &GeneratedCatalog{
		Version: 99, Format: SpecFormatOpenAPI3,
	}))
	require.NoError(t, f.Close())

	_, _, err = LoadGeneratedSpecSource(context.Background(), genPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "version")
}
