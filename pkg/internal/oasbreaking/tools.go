package oasbreaking

import (
	"slices"
	"strings"
)

// ToolStatus is how an MCP tool is affected between base and revision.
type ToolStatus string

const (
	// ToolRemoved is a tool generated from base but not from revision; any
	// client still calling it breaks, so it always counts as LevelErr.
	ToolRemoved ToolStatus = "removed"
	// ToolAdded is a tool generated only from revision.
	ToolAdded ToolStatus = "added"
	// ToolChanged is a tool on both sides with changes to its operation or
	// its generated inputSchema.
	ToolChanged ToolStatus = "changed"
)

// Notes GroupByTool attaches to a ToolImpact that oasdiff reported nothing for.
const (
	noteRemovedOnly = "no longer in the generated tools"
	noteAddedOnly   = "new in the generated tools"
	noteSchemaOnly  = "inputSchema changed in the generated tools, but oasdiff reported no change"
)

// CatalogTool is one generated MCP tool and the "METHOD /path" it calls.
type CatalogTool struct {
	Name      string
	Operation string
}

// CatalogDiff is how the generated tools themselves differ between base and
// revision, complementing what oasdiff finds in the specs.
type CatalogDiff struct {
	Added   []CatalogTool
	Removed []CatalogTool
	// SchemaChanged holds tools on both sides whose inputSchema differs.
	SchemaChanged []CatalogTool
}

// ToolImpact is every change affecting one MCP tool.
type ToolImpact struct {
	Name      string
	Operation string
	Status    ToolStatus
	// Level is the highest Level among Changes, raised to LevelErr for a
	// removed tool; LevelNone if only the generated inputSchema changed.
	Level Level
	// Note explains a status that no oasdiff change accounts for.
	Note    string
	Changes []Change
}

// ToolImpacts is the result of GroupByTool.
type ToolImpacts struct {
	// Tools holds each affected tool, most severe first, then by name.
	Tools []ToolImpact
	// SpecWide holds changes tied to no MCP tool: those outside paths
	// (components, security, api-version-not-bumped) and any on an
	// operation that isn't generated as a tool.
	SpecWide []Change
}

// MaxLevel returns the highest Level among r's tools and spec-wide changes.
func (r ToolImpacts) MaxLevel() Level {
	highest := MaxLevel(r.SpecWide)
	for _, t := range r.Tools {
		if t.Level > highest {
			highest = t.Level
		}
	}
	return highest
}

// GroupByTool groups changes (already annotated by ResolveTools) by MCP
// tool, merging in catalog so that a tool added, removed, or with a changed
// inputSchema is listed even when oasdiff reported nothing for it. Unaffected
// tools are omitted.
func GroupByTool(changes []Change, catalog CatalogDiff) ToolImpacts {
	var result ToolImpacts
	byName := map[string]*ToolImpact{}
	get := func(name, operation string) *ToolImpact {
		t, ok := byName[name]
		if !ok {
			t = &ToolImpact{Name: name, Operation: operation, Status: ToolChanged}
			byName[name] = t
		}
		return t
	}

	for _, c := range changes {
		if c.Tool == "" {
			result.SpecWide = append(result.SpecWide, c)
			continue
		}
		t := get(c.Tool, c.Operation)
		t.Changes = append(t.Changes, c)
	}
	for _, ct := range catalog.SchemaChanged {
		get(ct.Name, ct.Operation)
	}
	for _, ct := range catalog.Added {
		t := get(ct.Name, ct.Operation)
		t.Status = ToolAdded
		t.Operation = ct.Operation
	}
	for _, ct := range catalog.Removed {
		t := get(ct.Name, ct.Operation)
		t.Status = ToolRemoved
		t.Operation = ct.Operation
	}

	result.Tools = make([]ToolImpact, 0, len(byName))
	for _, t := range byName {
		t.Level = MaxLevel(t.Changes)
		if len(t.Changes) == 0 {
			switch t.Status {
			case ToolRemoved:
				t.Note = noteRemovedOnly
			case ToolAdded:
				t.Note = noteAddedOnly
			case ToolChanged:
				t.Note = noteSchemaOnly
			}
		}
		if t.Status == ToolRemoved {
			t.Level = LevelErr
		}
		result.Tools = append(result.Tools, *t)
	}
	slices.SortFunc(result.Tools, func(a, b ToolImpact) int {
		if a.Level != b.Level {
			return int(b.Level) - int(a.Level)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return result
}
