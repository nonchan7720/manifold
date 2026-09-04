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

// SpecSource is the result of fetching and loading a spec from specPath:
// its detected format, the sha256 of the raw bytes that were fetched, and
// the parsed spec (exactly one of OpenAPI/Swagger is set, matching Format).
type SpecSource struct {
	Format SpecFormat
	// SpecPath is the specPath LoadSpecSource was called with. It is kept
	// around because base URL derivation (GetBaseUrlFromOpenAPI3 /
	// GetBaseUrlFromSwagger) needs the original spec location, not just the
	// parsed spec.
	SpecPath string
	Hash     string
	OpenAPI  *openapi3.T
	Swagger  *openapi2.T
}

// LoadSpecSource fetches the spec at specPath exactly once, determines
// whether it is an OpenAPI 3.x or Swagger 2.x document, and parses those
// same fetched bytes with the corresponding loader (LoadOpenAPI3SpecFromData
// / ParseSwaggerSpec) — so the returned Hash always describes the exact
// document that was parsed, even for a remote spec that could otherwise
// change between a hashing fetch and a parsing fetch. This is the "spec 入
// 手" phase that RegisterOpenAPI performed inline before it was split out so
// it can be reused (e.g. by a future CLI) independently of catalog building.
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

// isSwaggerVersionProbe reports whether raw looks like a Swagger 2.x
// document (a non-empty top-level "swagger" field), trying JSON first and
// falling back to YAML so a YAML Swagger 2.x document is routed to the
// Swagger loader rather than the OpenAPI 3 loader. A document that decodes
// as neither is left for the OpenAPI 3 loader to produce the actual parse
// error for, matching the previous (JSON-only, error-ignoring) behavior.
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
