package mcpsrv

import (
	"context"
	"testing"

	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/stretchr/testify/require"
)

func TestDryRunCatalog_OpenAPI3(t *testing.T) {
	ctx := t.Context()
	source, err := oastomcptool.ParseSpecSource(
		ctx, []byte(inlineDefinitionsSpec), "inline.json", oastomcptool.SpecFormatOpenAPI3,
	)
	require.NoError(t, err)

	report, err := DryRunCatalog(ctx, source)
	require.NoError(t, err)
	require.Empty(t, report.Invalid)

	names := make([]string, 0, len(report.Tools))
	for _, d := range report.Tools {
		names = append(names, d.Name)
	}
	require.Equal(t, []string{"addpet", "downloadpetimage", "getpetbyid"}, names)
	// The spec has no servers entry, so the gateway would need baseURL.
	require.ErrorIs(t, report.BaseURLErr, errBaseURLUnresolved)
}

func TestDryRunCatalog_Swagger2_DerivesBaseURL(t *testing.T) {
	ctx := t.Context()
	raw := []byte(`{
  "swagger": "2.0",
  "info": {"title": "Legacy", "version": "1.0.0"},
  "host": "api.example.com",
  "basePath": "/v1",
  "schemes": ["https"],
  "paths": {
    "/legacy": {"get": {"operationId": "getLegacy", "responses": {"200": {"description": "ok"}}}}
  }
}`)
	source, err := oastomcptool.ParseSpecSource(
		ctx, raw, "legacy.json", oastomcptool.SpecFormatSwagger2,
	)
	require.NoError(t, err)

	report, err := DryRunCatalog(ctx, source)
	require.NoError(t, err)
	require.NoError(t, report.BaseURLErr)
	require.Equal(t, "https://api.example.com/v1", report.BaseURL)
	require.Len(t, report.Tools, 1)
	require.Equal(t, "getlegacy", report.Tools[0].Name)
	require.Equal(t, "GET", report.Tools[0].Method)
}

func TestDryRunRegistry_ReportsToolsTheGatewayWouldSkip(t *testing.T) {
	noop := ToolFunc(func(context.Context, map[string]any) ([]byte, string, error) {
		return nil, "", nil
	})
	registry := NewMCPToolRegistry()
	registry.setBaseURL("https://api.example.com")
	registry.RegisterTool("good", "ok", map[string]any{"type": "object"}, noop,
		WithRegisterToolOperation("get", "/good"))
	registry.RegisterTool("bad", "not an object schema", map[string]any{"type": "string"}, noop,
		WithRegisterToolOperation("get", "/bad"))

	report := dryRunRegistry(registry)

	require.NoError(t, report.BaseURLErr)
	require.Len(t, report.Tools, 1)
	require.Equal(t, "good", report.Tools[0].Name)
	require.Len(t, report.Invalid, 1)
	require.Equal(t, "bad", report.Invalid[0].Name)
	require.ErrorContains(t, report.Invalid[0].Err, `input schema must have type "object"`)
}
