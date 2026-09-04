package oastomcptool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/n-creativesystem/go-packages/lib/trace"
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

// LoadSpecSource fetches the spec at specPath, determines whether it is an
// OpenAPI 3.x or Swagger 2.x document, and loads it with the corresponding
// loader (LoadOpenAPI3Spec / LoadSwaggerSpec). This is the "spec 入手" phase
// that RegisterOpenAPI performed inline before it was split out so it can be
// reused (e.g. by a future CLI) independently of catalog building.
func LoadSpecSource(ctx context.Context, specPath string) (_ *SpecSource, rErr error) {
	ctx = trace.StartSpan(ctx, "oastomcptool/LoadSpecSource")
	defer func() { trace.EndSpan(ctx, rErr) }()

	raw, err := FetchSpecBytes(ctx, specPath)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))

	// バージョン判定のため最小限の JSON デコード
	var versionProbe struct {
		Swagger string `json:"swagger"`
	}
	_ = json.Unmarshal(raw, &versionProbe)

	if versionProbe.Swagger != "" {
		spec, err := LoadSwaggerSpec(ctx, specPath)
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

	spec, err := LoadOpenAPI3Spec(specPath)
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
