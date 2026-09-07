package mcpsrv

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
)

// GeneratedTools converts defs into the GeneratedTool shape written to a
// generated tools file's "tools" section.
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

// VerifyGeneratedTools returns an error naming the first difference between
// registry's built tools and g's "tools" section.
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
		same, err := EqualAsJSON(gt.InputSchema, d.InputSchema)
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

// GeneratedToolChange is one tool whose operation, description,
// binaryResponse, or inputSchema differ between two DiffGeneratedTools sides.
type GeneratedToolChange struct {
	Name   string
	Fields []string
}

// GeneratedToolsDiff is the result of comparing two generated tool lists by name.
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

// DiffGeneratedTools compares current against next by name, returning the
// tools added, removed, or changed; results are not sorted.
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

// changedGeneratedToolFields returns which fields differ between a and b.
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
	if same, err := EqualAsJSON(a.InputSchema, b.InputSchema); err != nil || !same {
		fields = append(fields, "inputSchema")
	}
	return fields
}

// EqualAsJSON reports whether a and b are equal after canonical JSON
// encoding, so YAML-decoded ints and JSON floats don't cause a false mismatch.
func EqualAsJSON(a, b any) (bool, error) {
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
