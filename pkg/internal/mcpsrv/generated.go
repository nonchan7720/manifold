package mcpsrv

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
)

// GeneratedTools converts a registry's ToolDefinition list into the
// oastomcptool.GeneratedTool shape written to a generated tools file's
// "tools" section (Operation is "METHOD path", e.g. "GET /pet/{petId}").
func GeneratedTools(defs []ToolDefinition) []oastomcptool.GeneratedTool {
	tools := make([]oastomcptool.GeneratedTool, len(defs))
	for i, d := range defs {
		tools[i] = oastomcptool.GeneratedTool{
			Name:           d.Name,
			Operation:      fmt.Sprintf("%s %s", d.Method, d.Path),
			Description:    d.Description,
			BinaryResponse: d.BinaryResponse,
			InputSchema:    d.InputSchema,
		}
	}
	return tools
}

// VerifyGeneratedTools compares the tools registry actually built from a
// generated catalog's internalized spec against that catalog's "tools"
// section (the human-readable listing a generated file carries alongside
// its spec). Any difference — a tool only on one side, or a differing
// operation/description/binaryResponse/inputSchema — means the file is
// stale relative to what its own spec now produces, and is reported with
// the specific reason so the operator knows what to look at.
func VerifyGeneratedTools(registry *MCPToolRegistry, g *oastomcptool.GeneratedCatalog) error {
	built := registry.Definitions()
	byName := make(map[string]ToolDefinition, len(built))
	for _, d := range built {
		byName[d.Name] = d
	}

	seen := make(map[string]struct{}, len(g.Tools))
	for _, gt := range g.Tools {
		if _, dup := seen[gt.Name]; dup {
			return fmt.Errorf(
				"generated tools are stale: duplicate tool %q in generated file", gt.Name,
			)
		}
		seen[gt.Name] = struct{}{}
		d, ok := byName[gt.Name]
		if !ok {
			return fmt.Errorf("generated tools are stale: tool %q not produced by spec", gt.Name)
		}
		if op := fmt.Sprintf("%s %s", d.Method, d.Path); gt.Operation != op {
			return fmt.Errorf("generated tools are stale: tool %q operation differs", gt.Name)
		}
		if gt.Description != d.Description {
			return fmt.Errorf("generated tools are stale: tool %q description differs", gt.Name)
		}
		if gt.BinaryResponse != d.BinaryResponse {
			return fmt.Errorf("generated tools are stale: tool %q binaryResponse differs", gt.Name)
		}
		same, err := equalAsJSON(gt.InputSchema, d.InputSchema)
		if err != nil {
			return fmt.Errorf("generated tools are stale: tool %q inputSchema: %w", gt.Name, err)
		}
		if !same {
			return fmt.Errorf("generated tools are stale: tool %q inputSchema differs", gt.Name)
		}
	}

	for name := range byName {
		if _, ok := seen[name]; !ok {
			return fmt.Errorf(
				"generated tools are stale: tool %q missing from generated file", name,
			)
		}
	}
	return nil
}

// equalAsJSON compares a and b by their canonical JSON encoding (which sorts
// object keys), so YAML-decoded numeric types (int) and the float64 that
// encoding/json produces for the same value don't cause a false mismatch.
func equalAsJSON(a, b any) (bool, error) {
	ab, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ab, bb), nil
}
