package oastomcptool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/goccy/go-yaml"
	"github.com/n-creativesystem/go-packages/lib/trace"
)

// GeneratedVersion is the format version of the generated tools file this package writes and reads.
const GeneratedVersion = 1

// GeneratedTool is one entry of a generated catalog's "tools" section.
type GeneratedTool struct {
	Name           string         `yaml:"name"`
	Operation      string         `yaml:"operation"`
	Description    string         `yaml:"description"`
	BinaryResponse bool           `yaml:"binaryResponse"`
	InputSchema    map[string]any `yaml:"inputSchema"`
}

// GeneratedSource records where a generated catalog's spec came from.
type GeneratedSource struct {
	Spec      string `yaml:"spec"`
	SHA256    string `yaml:"sha256"`
	FetchedAt string `yaml:"fetchedAt"`
}

// GeneratedCatalog is the in-memory form of a generated tools file. Spec is
// the authoritative document; Tools is a derived listing verified against it.
type GeneratedCatalog struct {
	Version     int             `yaml:"version"`
	GeneratedBy string          `yaml:"generatedBy"`
	Source      GeneratedSource `yaml:"source"`
	Format      SpecFormat      `yaml:"format"`
	Tools       []GeneratedTool `yaml:"tools"`
	Spec        map[string]any  `yaml:"spec"`
}

// NewGeneratedCatalog builds a GeneratedCatalog from an already-loaded
// OpenAPI 3.x or Swagger 2.x source. An OpenAPI 3.x source has its external
// $refs internalized in the process.
//
// Swagger 2.x の文書は変換せず、そのまま埋め込む。ゲートウェイは起動時にこの文書から
// Swagger 用の経路でツールを作り直すので、spec から直接起動したときと同じツールになる。
func NewGeneratedCatalog(
	ctx context.Context,
	source *SpecSource,
	tools []GeneratedTool,
	generatedBy string,
	fetchedAt time.Time,
) (_ *GeneratedCatalog, rErr error) {
	ctx = trace.StartSpan(ctx, "oastomcptool/NewGeneratedCatalog")
	defer func() { trace.EndSpan(ctx, rErr) }()

	specMap, err := generatedSpecSection(ctx, source)
	if err != nil {
		return nil, err
	}

	return &GeneratedCatalog{
		Version:     GeneratedVersion,
		GeneratedBy: generatedBy,
		Source: GeneratedSource{
			Spec:      source.SpecPath,
			SHA256:    source.Hash,
			FetchedAt: fetchedAt.UTC().Format(time.RFC3339),
		},
		Format: source.Format,
		Tools:  tools,
		Spec:   specMap,
	}, nil
}

// generatedSpecSection は source の文書を、生成ファイルの "spec" 節に入れる形
// （JSON と同じ構造の map）に変換する。
func generatedSpecSection(ctx context.Context, source *SpecSource) (map[string]any, error) {
	var doc any
	switch {
	case source.Format == SpecFormatOpenAPI3 && source.OpenAPI != nil:
		// Mutates the doc in place; fine here since the caller doesn't reuse source.
		source.OpenAPI.InternalizeRefs(ctx, nil)
		doc = source.OpenAPI
	case source.Format == SpecFormatSwagger2 && source.Swagger != nil:
		// Swagger 2.x の $ref は文書内（#/definitions/... と #/parameters/...）しか
		// 解決しないので、内部化は要らない。
		doc = source.Swagger
	default:
		return nil, fmt.Errorf(
			"generated tools file: unsupported spec format %q (want %q or %q)",
			source.Format, SpecFormatOpenAPI3, SpecFormatSwagger2,
		)
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	var specMap map[string]any
	if err := json.Unmarshal(raw, &specMap); err != nil {
		return nil, fmt.Errorf("decode spec: %w", err)
	}
	return specMap, nil
}

// WriteGeneratedCatalog encodes g as YAML (2-space indent) to w.
func WriteGeneratedCatalog(w io.Writer, g *GeneratedCatalog) (rErr error) {
	enc := yaml.NewEncoder(w, yaml.Indent(2), yaml.IndentSequence(true))
	defer func() {
		if err := enc.Close(); rErr == nil {
			rErr = err
		}
	}()
	return enc.Encode(g)
}

// ReadGeneratedCatalog decodes a generated tools file from r, rejecting an
// unknown version or format.
func ReadGeneratedCatalog(r io.Reader) (*GeneratedCatalog, error) {
	var g GeneratedCatalog
	if err := yaml.NewDecoder(r).Decode(&g); err != nil {
		return nil, fmt.Errorf("decode generated tools file: %w", err)
	}
	if g.Version != GeneratedVersion {
		return nil, fmt.Errorf(
			"generated tools file: unsupported version %d (this build supports %d)",
			g.Version, GeneratedVersion,
		)
	}
	if g.Format != SpecFormatOpenAPI3 && g.Format != SpecFormatSwagger2 {
		return nil, fmt.Errorf(
			"generated tools file: unsupported format %q (this build supports %q and %q)",
			g.Format, SpecFormatOpenAPI3, SpecFormatSwagger2,
		)
	}
	return &g, nil
}

// LoadGeneratedSpecSource reads and validates the generated tools file at
// path, and rebuilds a *SpecSource from its "spec" section.
// External $refs are disallowed here so loading from a generated file can
// never reach the network.
func LoadGeneratedSpecSource(
	ctx context.Context, path string,
) (_ *SpecSource, _ *GeneratedCatalog, rErr error) {
	ctx = trace.StartSpan(ctx, "oastomcptool/LoadGeneratedSpecSource")
	defer func() { trace.EndSpan(ctx, rErr) }()

	raw, err := os.ReadFile(path) //nolint: gosec
	if err != nil {
		return nil, nil, fmt.Errorf("read generated tools file: %w", err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))

	g, err := ReadGeneratedCatalog(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}

	source := &SpecSource{
		Format:   g.Format,
		SpecPath: g.Source.Spec,
		Hash:     hash,
	}
	// g.Format は ReadGeneratedCatalog で検証済み。
	switch g.Format {
	case SpecFormatSwagger2:
		source.Swagger, err = loadGeneratedSwaggerSpec(ctx, g)
	case SpecFormatOpenAPI3:
		source.OpenAPI, err = loadGeneratedOpenAPI3Spec(g)
	}
	if err != nil {
		return nil, nil, err
	}
	return source, g, nil
}

// LoadGeneratedSpec loads g's "spec" section as an OpenAPI 3.x document, for
// diffing with oasdiff. A Swagger 2.x section is converted to OpenAPI 3.x
// first; the gateway itself never serves tools from this converted form.
// External $refs are disallowed, so this never reaches the network.
func LoadGeneratedSpec(ctx context.Context, g *GeneratedCatalog) (*openapi3.T, error) {
	if g.Format != SpecFormatSwagger2 {
		return loadGeneratedOpenAPI3Spec(g)
	}
	spec, err := loadGeneratedSwaggerSpec(ctx, g)
	if err != nil {
		return nil, err
	}
	// ToV3 の既定の Loader は外部 $ref を許可しないので、ネットワークには出ない。
	doc, err := openapi2conv.ToV3(spec)
	if err != nil {
		return nil, fmt.Errorf("convert generated swagger2 spec section to openapi3: %w", err)
	}
	return doc, nil
}

// loadGeneratedOpenAPI3Spec は g の "spec" 節を OpenAPI 3.x の文書として読む。
func loadGeneratedOpenAPI3Spec(g *GeneratedCatalog) (*openapi3.T, error) {
	specJSON, err := json.Marshal(g.Spec)
	if err != nil {
		return nil, fmt.Errorf("marshal generated spec section: %w", err)
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromData(specJSON)
	if err != nil {
		return nil, fmt.Errorf("load generated spec section: %w", err)
	}
	return doc, nil
}

// loadGeneratedSwaggerSpec は g の "spec" 節を Swagger 2.x の文書として読む。
// Swagger 2.x の経路は外部 $ref を辿らないので、ネットワークには出ない。
func loadGeneratedSwaggerSpec(ctx context.Context, g *GeneratedCatalog) (*openapi2.T, error) {
	specJSON, err := json.Marshal(g.Spec)
	if err != nil {
		return nil, fmt.Errorf("marshal generated spec section: %w", err)
	}
	spec, err := ParseSwaggerSpec(ctx, specJSON)
	if err != nil {
		return nil, fmt.Errorf("load generated spec section: %w", err)
	}
	return spec, nil
}
