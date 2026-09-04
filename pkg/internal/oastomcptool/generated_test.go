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

func TestNewGeneratedCatalog_RejectsSwagger2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swagger.json")
	require.NoError(t, os.WriteFile(path, []byte(inlineSwagger2Spec), 0o600))
	source, err := LoadSpecSource(context.Background(), path)
	require.NoError(t, err)

	_, err = NewGeneratedCatalog(context.Background(), source, nil, "manifold test", time.Now())
	require.Error(t, err)
	require.ErrorContains(t, err, "swagger2")
}

func TestReadGeneratedCatalog_RejectsUnknownVersion(t *testing.T) {
	g := &GeneratedCatalog{Version: 2, Format: SpecFormatOpenAPI3}
	var buf bytes.Buffer
	require.NoError(t, WriteGeneratedCatalog(&buf, g))

	_, err := ReadGeneratedCatalog(&buf)
	require.Error(t, err)
	require.ErrorContains(t, err, "version")
}

func TestReadGeneratedCatalog_RejectsSwagger2Format(t *testing.T) {
	g := &GeneratedCatalog{Version: GeneratedVersion, Format: SpecFormatSwagger2}
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
