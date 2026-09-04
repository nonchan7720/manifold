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

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/n-creativesystem/go-packages/lib/trace"
	yaml "go.yaml.in/yaml/v3"
)

// GeneratedVersion is the format version of the generated tools file this
// package writes and reads. ReadGeneratedCatalog rejects any other value.
const GeneratedVersion = 1

// GeneratedTool is one entry of a generated catalog's "tools" section — a
// human-readable, diffable projection of one MCP tool. It is derived data:
// the authoritative source is Spec, and the loader re-derives the catalog
// from it at load time and compares (see mcpsrv.VerifyGeneratedTools).
type GeneratedTool struct {
	Name           string         `yaml:"name"`
	Operation      string         `yaml:"operation"`
	Description    string         `yaml:"description"`
	BinaryResponse bool           `yaml:"binaryResponse"`
	InputSchema    map[string]any `yaml:"inputSchema"`
}

// GeneratedSource records where a generated catalog's spec came from and
// what it looked like at generation time.
type GeneratedSource struct {
	Spec      string `yaml:"spec"`
	SHA256    string `yaml:"sha256"`
	FetchedAt string `yaml:"fetchedAt"`
}

// GeneratedCatalog is the in-memory form of a generated tools file (see
// docs/design/openapi-static-catalog.ja.md, 「生成物の形式」). Field order
// matches the struct field order (yaml.v3 preserves declaration order for
// unkeyed structs): version, generatedBy, source, format, tools, spec.
// Spec is the authoritative document (external $refs already internalized);
// Tools is a derived, human-readable listing kept in sync by verification
// at load time, never used to build the runtime catalog itself.
type GeneratedCatalog struct {
	Version     int             `yaml:"version"`
	GeneratedBy string          `yaml:"generatedBy"`
	Source      GeneratedSource `yaml:"source"`
	Format      SpecFormat      `yaml:"format"`
	Tools       []GeneratedTool `yaml:"tools"`
	Spec        map[string]any  `yaml:"spec"`
}

// NewGeneratedCatalog builds a GeneratedCatalog from an already-loaded
// source: it internalizes source.OpenAPI's external $refs in place
// (openapi3.T.InternalizeRefs with the library's default resolver), then
// serializes the result through JSON into a plain map[string]any so it
// round-trips through YAML as plain maps (rather than depending on
// openapi3.T's own MarshalJSON at read time, which LoadGeneratedSpecSource
// does not have access to). Phase 1 only supports OpenAPI 3.x; a
// SpecFormatSwagger2 source is an error.
func NewGeneratedCatalog(
	ctx context.Context,
	source *SpecSource,
	tools []GeneratedTool,
	generatedBy string,
	fetchedAt time.Time,
) (_ *GeneratedCatalog, rErr error) {
	ctx = trace.StartSpan(ctx, "oastomcptool/NewGeneratedCatalog")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if source.Format != SpecFormatOpenAPI3 || source.OpenAPI == nil {
		return nil, fmt.Errorf(
			"generated tools file: unsupported spec format %q (Phase 1 supports openapi3 only)",
			source.Format,
		)
	}

	// InternalizeRefs mutates the doc in place. This is fine here: the CLI
	// (the only caller that builds generated catalogs) doesn't reuse source
	// after this call.
	source.OpenAPI.InternalizeRefs(ctx, nil)

	raw, err := json.Marshal(source.OpenAPI)
	if err != nil {
		return nil, fmt.Errorf("marshal internalized spec: %w", err)
	}
	var specMap map[string]any
	if err := json.Unmarshal(raw, &specMap); err != nil {
		return nil, fmt.Errorf("decode internalized spec: %w", err)
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

// WriteGeneratedCatalog encodes g as YAML (2-space indent) to w.
func WriteGeneratedCatalog(w io.Writer, g *GeneratedCatalog) (rErr error) {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer func() {
		if err := enc.Close(); rErr == nil {
			rErr = err
		}
	}()
	return enc.Encode(g)
}

// ReadGeneratedCatalog decodes a generated tools file from r and rejects an
// unknown version or format up front, before any caller tries to use Spec
// or Tools.
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
	if g.Format != SpecFormatOpenAPI3 {
		return nil, fmt.Errorf(
			"generated tools file: unsupported format %q (this build supports %q)",
			g.Format, SpecFormatOpenAPI3,
		)
	}
	return &g, nil
}

// LoadGeneratedSpecSource reads and validates the generated tools file at
// path, and rebuilds a *SpecSource from its "spec" section: Format is
// always openapi3, SpecPath is the recorded source.spec (for base URL
// derivation, matching LoadSpecSource), Hash is the sha256 of the file's
// own bytes (not source.sha256 — this is what the gateway compares across
// refresh-free restarts), and OpenAPI is loaded with external $refs
// disallowed. That last part is the structural guarantee that starting from
// a generated file never reaches the network: any leftover external $ref
// (internalization bug, hand-edited file) is a load error here rather than
// a silent fetch.
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

	specJSON, err := json.Marshal(g.Spec)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal generated spec section: %w", err)
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromData(specJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("load generated spec section: %w", err)
	}

	return &SpecSource{
		Format:   SpecFormatOpenAPI3,
		SpecPath: g.Source.Spec,
		Hash:     hash,
		OpenAPI:  doc,
	}, g, nil
}
