package oastomcptool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpecFormatFromFileName(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		want   SpecFormat
		wantOK bool
	}{
		{"openapi yaml", "petstore.openapi.yaml", SpecFormatOpenAPI3, true},
		{"openapi yml", "dir/petstore.openapi.yml", SpecFormatOpenAPI3, true},
		{"swagger yaml", "petstore.swagger.yaml", SpecFormatSwagger2, true},
		{"swagger yml", "/abs/petstore.swagger.yml", SpecFormatSwagger2, true},
		{"case insensitive", "Petstore.OpenAPI.YAML", SpecFormatOpenAPI3, true},
		{"plain yaml", "petstore.yaml", "", false},
		{"json is not a suffix the name declares", "petstore.openapi.json", "", false},
		{"bare openapi.yaml has no name part", "openapi.yaml", "", false},
		{"marker not right before extension", "openapi.petstore.yaml", "", false},
		{"directory name is ignored", "x.openapi.yaml/petstore.yaml", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SpecFormatFromFileName(tt.path)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDetectSpecFormat(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantFormat  SpecFormat
		wantVersion string
	}{
		{"openapi 3.0 yaml", "openapi: 3.0.3\ninfo: {}\n", SpecFormatOpenAPI3, "3.0.3"},
		{"openapi 3.1 yaml", "openapi: \"3.1.0\"\n", SpecFormatOpenAPI3, "3.1.0"},
		{"openapi json", `{"openapi": "3.0.0", "paths": {}}`, SpecFormatOpenAPI3, "3.0.0"},
		{"swagger quoted", "swagger: \"2.0\"\n", SpecFormatSwagger2, "2.0"},
		{"swagger json number", `{"swagger": 2.0}`, SpecFormatSwagger2, "2.0"},
		{"swagger yaml number", "swagger: 2.0\n", SpecFormatSwagger2, "2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format, version, err := DetectSpecFormat([]byte(tt.raw))
			require.NoError(t, err)
			require.Equal(t, tt.wantFormat, format)
			require.Equal(t, tt.wantVersion, version)
		})
	}
}

func TestDetectSpecFormat_Errors(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"no version field", "info: {title: x}\n", `neither an "openapi" nor a "swagger" field`},
		{"empty document", "", `neither an "openapi" nor a "swagger" field`},
		{"broken yaml", "openapi: 3.0.0\npaths: [\n", "parse YAML"},
		{"top-level sequence", "- openapi: 3.0.0\n", "document is not a mapping (got array)"},
		{"top-level scalar", "just a string\n", "document is not a mapping (got string)"},
		{"both fields", "openapi: 3.0.0\nswagger: \"2.0\"\n", `both "openapi"`},
		{"openapi 2.x", "openapi: \"2.0\"\n", `unsupported OpenAPI version "2.0"`},
		{"swagger 3.x", "swagger: \"3.0\"\n", `unsupported Swagger version "3.0"`},
		{"openapi 30 is not 3.x", "openapi: \"30.0\"\n", `unsupported OpenAPI version "30.0"`},
		{"non-scalar version", "openapi: {major: 3}\n", `"openapi" field must be a version string`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := DetectSpecFormat([]byte(tt.raw))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestParseSpecSource_UsesGivenFormat(t *testing.T) {
	ctx := t.Context()

	openAPI, err := ParseSpecSource(
		ctx,
		[]byte(inlineOpenAPI3Spec),
		"inline.json",
		SpecFormatOpenAPI3,
	)
	require.NoError(t, err)
	require.Equal(t, SpecFormatOpenAPI3, openAPI.Format)
	require.NotNil(t, openAPI.OpenAPI)
	require.Nil(t, openAPI.Swagger)
	require.Equal(t, "inline.json", openAPI.SpecPath)
	require.NotEmpty(t, openAPI.Hash)

	swagger, err := ParseSpecSource(
		ctx,
		[]byte(inlineSwagger2Spec),
		"inline.json",
		SpecFormatSwagger2,
	)
	require.NoError(t, err)
	require.Equal(t, SpecFormatSwagger2, swagger.Format)
	require.NotNil(t, swagger.Swagger)
	require.Nil(t, swagger.OpenAPI)

	_, err = ParseSpecSource(ctx, []byte(inlineOpenAPI3Spec), "inline.json", SpecFormat("bogus"))
	require.ErrorContains(t, err, `unsupported spec format: "bogus"`)
}
