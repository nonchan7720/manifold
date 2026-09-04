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

// GeneratedToolChange is one tool present on both sides of a
// DiffGeneratedTools comparison whose operation, description,
// binaryResponse, or inputSchema differ. Fields names which, in check order
// (operation, description, binaryResponse, inputSchema); at least one entry
// is always present.
type GeneratedToolChange struct {
	Name   string
	Fields []string
}

// GeneratedToolsDiff is the result of comparing two generated tool lists
// (e.g. an on-disk generated file's "tools" section against a freshly built
// catalog) tool-by-tool, matched by name.
type GeneratedToolsDiff struct {
	// Added holds tools present in "next" but not "current".
	Added []oastomcptool.GeneratedTool
	// Removed holds tools present in "current" but not "next".
	Removed []oastomcptool.GeneratedTool
	// Changed holds tools present on both sides whose fields differ.
	Changed []GeneratedToolChange
}

// Empty reports whether the diff found no differences at all.
func (d GeneratedToolsDiff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// DiffGeneratedTools compares current against next, matching tools by name:
// a tool only in next is Added, a tool only in current is Removed, and a
// tool in both whose operation, description, binaryResponse, or inputSchema
// differ is Changed (naming which fields differ). inputSchema is compared
// by canonical JSON encoding (see equalAsJSON), so a YAML-decoded int and
// the float64 encoding/json produces for the same number don't register as
// a difference. A duplicate name within either slice is resolved by taking
// the last occurrence, matching how a map keyed by name would see it.
// Results are not sorted; a caller that needs a deterministic order (e.g.
// CLI output) should sort them itself.
func DiffGeneratedTools(current, next []oastomcptool.GeneratedTool) GeneratedToolsDiff {
	curByName := make(map[string]oastomcptool.GeneratedTool, len(current))
	for _, t := range current {
		curByName[t.Name] = t
	}
	nextByName := make(map[string]oastomcptool.GeneratedTool, len(next))
	for _, t := range next {
		nextByName[t.Name] = t
	}

	var diff GeneratedToolsDiff
	for _, t := range next {
		if _, ok := curByName[t.Name]; !ok {
			diff.Added = append(diff.Added, t)
		}
	}
	for _, t := range current {
		if _, ok := nextByName[t.Name]; !ok {
			diff.Removed = append(diff.Removed, t)
		}
	}
	for name, c := range curByName {
		n, ok := nextByName[name]
		if !ok {
			continue
		}
		if fields := changedGeneratedToolFields(c, n); len(fields) > 0 {
			diff.Changed = append(diff.Changed, GeneratedToolChange{Name: name, Fields: fields})
		}
	}
	return diff
}

// changedGeneratedToolFields returns which of operation, description,
// binaryResponse, and inputSchema differ between a and b, in that order.
func changedGeneratedToolFields(a, b oastomcptool.GeneratedTool) []string {
	var fields []string
	if a.Operation != b.Operation {
		fields = append(fields, "operation")
	}
	if a.Description != b.Description {
		fields = append(fields, "description")
	}
	if a.BinaryResponse != b.BinaryResponse {
		fields = append(fields, "binaryResponse")
	}
	if same, err := equalAsJSON(a.InputSchema, b.InputSchema); err != nil || !same {
		fields = append(fields, "inputSchema")
	}
	return fields
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
