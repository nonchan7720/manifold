package oastomcptool

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	oasyaml "github.com/oasdiff/yaml"
)

// SpecFormatFromFileName returns the format a file name declares by its
// "<name>.openapi.{yaml,yml}" or "<name>.swagger.{yaml,yml}" suffix (case
// insensitive), and false for any other name.
func SpecFormatFromFileName(path string) (SpecFormat, bool) {
	base := strings.ToLower(filepath.Base(path))
	for _, ext := range []string{".yaml", ".yml"} {
		stem, ok := strings.CutSuffix(base, ext)
		if !ok {
			continue
		}
		switch {
		case strings.HasSuffix(stem, ".openapi"):
			return SpecFormatOpenAPI3, true
		case strings.HasSuffix(stem, ".swagger"):
			return SpecFormatSwagger2, true
		}
	}
	return "", false
}

// errNoSpecVersion marks a document with neither an "openapi" nor a
// "swagger" top-level field.
var errNoSpecVersion = errors.New(`document has neither an "openapi" nor a "swagger" field`)

// DetectSpecFormat reads the top-level "openapi" / "swagger" version field
// of raw (YAML or JSON) and returns the format it declares, with the version
// as written. Only OpenAPI 3.x and Swagger 2.x are accepted.
func DetectSpecFormat(raw []byte) (SpecFormat, string, error) {
	data := raw
	if !json.Valid(raw) {
		// kin-openapi も oasdiff/yaml で YAML を読むため、同じ変換で判定する。
		converted, err := oasyaml.YAMLToJSON(raw)
		if err != nil {
			return "", "", fmt.Errorf("parse YAML: %w", err)
		}
		data = converted
	}

	var probe struct {
		OpenAPI any `json:"openapi"`
		Swagger any `json:"swagger"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// 2.0 や 3.0 を数値で書いた spec でも、書かれたとおりの表記を残す。
	dec.UseNumber()
	if err := dec.Decode(&probe); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && typeErr.Field == "" {
			return "", "", fmt.Errorf("document is not a mapping (got %s)", typeErr.Value)
		}
		return "", "", fmt.Errorf("parse document: %w", err)
	}

	openAPIVersion, err := specVersionString("openapi", probe.OpenAPI)
	if err != nil {
		return "", "", err
	}
	swaggerVersion, err := specVersionString("swagger", probe.Swagger)
	if err != nil {
		return "", "", err
	}

	switch {
	case openAPIVersion != "" && swaggerVersion != "":
		return "", "", fmt.Errorf(
			`document has both "openapi" (%q) and "swagger" (%q) fields`,
			openAPIVersion, swaggerVersion,
		)
	case openAPIVersion != "":
		if !hasMajorVersion(openAPIVersion, "3") {
			return "", "", fmt.Errorf("unsupported OpenAPI version %q (want 3.x)", openAPIVersion)
		}
		return SpecFormatOpenAPI3, openAPIVersion, nil
	case swaggerVersion != "":
		if !hasMajorVersion(swaggerVersion, "2") {
			return "", "", fmt.Errorf("unsupported Swagger version %q (want 2.x)", swaggerVersion)
		}
		return SpecFormatSwagger2, swaggerVersion, nil
	default:
		return "", "", errNoSpecVersion
	}
}

// specVersionString returns the version field v as a string, or "" if it
// is absent.
func specVersionString(field string, v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	default:
		return "", fmt.Errorf("%q field must be a version string (got %T)", field, v)
	}
}

// hasMajorVersion reports whether version is major itself or starts with
// "<major>.".
func hasMajorVersion(version, major string) bool {
	return version == major || strings.HasPrefix(version, major+".")
}
