package oastomcptool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/n-creativesystem/go-packages/lib/trace"
	yaml "go.yaml.in/yaml/v3"
)

// SpecFormat is the detected format of a fetched spec.
type SpecFormat string

const (
	SpecFormatOpenAPI3 SpecFormat = "openapi3"
	SpecFormatSwagger2 SpecFormat = "swagger2"
)

// SpecSource is the result of fetching and loading a spec from specPath.
type SpecSource struct {
	Format SpecFormat
	// SpecPath is kept for base URL derivation, which needs the original
	// spec location, not just the parsed spec.
	SpecPath string
	Hash     string
	OpenAPI  *openapi3.T
	Swagger  *openapi2.T
}

// LoadSpecSource fetches the spec at specPath exactly once and parses it as
// either OpenAPI 3.x or Swagger 2.x.
func LoadSpecSource(ctx context.Context, specPath string) (_ *SpecSource, rErr error) {
	ctx = trace.StartSpan(ctx, "oastomcptool/LoadSpecSource")
	defer func() { trace.EndSpan(ctx, rErr) }()

	raw, err := FetchSpecBytes(ctx, specPath)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))

	if isSwaggerVersionProbe(raw) {
		spec, err := ParseSwaggerSpec(ctx, raw)
		if err != nil {
			return nil, err
		}
		return &SpecSource{
			Format:   SpecFormatSwagger2,
			SpecPath: specPath,
			Hash:     hash,
			Swagger:  spec,
		}, nil
	}

	spec, err := LoadOpenAPI3SpecFromData(raw, specPath)
	if err != nil {
		return nil, err
	}
	return &SpecSource{
		Format:   SpecFormatOpenAPI3,
		SpecPath: specPath,
		Hash:     hash,
		OpenAPI:  spec,
	}, nil
}

// isSwaggerVersionProbe reports whether raw has a non-empty top-level
// "swagger" field, trying JSON then YAML.
func isSwaggerVersionProbe(raw []byte) bool {
	var probe struct {
		Swagger string `json:"swagger" yaml:"swagger"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil {
		return probe.Swagger != ""
	}
	if err := yaml.Unmarshal(raw, &probe); err == nil {
		return probe.Swagger != ""
	}
	return false
}
