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
// OpenAPI 3.x source, internalizing its external $refs in the process.
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

	// Mutates the doc in place; fine here since the caller doesn't reuse source.
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
	if g.Format != SpecFormatOpenAPI3 {
		return nil, fmt.Errorf(
			"generated tools file: unsupported format %q (this build supports %q)",
			g.Format, SpecFormatOpenAPI3,
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
