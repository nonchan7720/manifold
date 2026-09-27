// Package oasbreaking wraps oasdiff to report the changes between two
// OpenAPI 3.x documents, classified by how badly they break existing clients.
package oasbreaking

import (
	"fmt"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oasdiff/oasdiff/checker"
	"github.com/oasdiff/oasdiff/diff"
	"github.com/oasdiff/oasdiff/load"
)

// Level is the severity of a Change. The zero value, LevelNone, is below
// every real change and so, as a threshold, never matches one.
type Level int

const (
	LevelNone Level = iota
	LevelInfo
	LevelWarn
	LevelErr
)

// ParseLevel parses a --fail-on style level: "ERR", "WARN", "INFO"
// (case-insensitive), or "" / "NONE" for LevelNone.
func ParseLevel(s string) (Level, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ERR", "ERROR":
		return LevelErr, nil
	case "WARN", "WARNING":
		return LevelWarn, nil
	case "INFO":
		return LevelInfo, nil
	case "", "NONE":
		return LevelNone, nil
	}
	return LevelNone, fmt.Errorf("invalid level %q (want ERR, WARN, INFO or NONE)", s)
}

// String returns the lower-case display name of l ("error", "warning", "info").
func (l Level) String() string {
	switch l {
	case LevelErr:
		return "error"
	case LevelWarn:
		return "warning"
	case LevelInfo:
		return "info"
	case LevelNone:
		return "none"
	}
	return "unknown"
}

// Breaking reports whether l breaks existing clients (warning or error).
func (l Level) Breaking() bool {
	return l >= LevelWarn
}

// MarshalText encodes l by its display name, so JSON output reads "error"
// rather than 3.
func (l Level) MarshalText() ([]byte, error) {
	return []byte(l.String()), nil
}

// Change is one difference oasdiff found between base and revision.
type Change struct {
	Level Level `json:"level"`
	// ID is the oasdiff rule id, e.g. "request-parameter-became-required".
	ID string `json:"id"`
	// Operation is "METHOD /path", or empty for a change outside paths
	// (e.g. a component or security scheme).
	Operation string `json:"operation,omitempty"`
	Message   string `json:"message"`
	// Tool is the MCP tool name for Operation; left empty by Check and filled
	// in by ResolveTools.
	Tool string `json:"tool,omitempty"`
}

// Check diffs base against revision and returns every change at LevelInfo
// or above, most severe first (oasdiff's ordering within a level). Both
// documents must already have their $refs resolved, as openapi3.Loader does.
func Check(base, revision *openapi3.T) ([]Change, error) {
	d, sources, err := diff.GetWithOperationsSourcesMap(
		diff.NewConfig(),
		&load.SpecInfo{Url: "base", Spec: base},
		&load.SpecInfo{Url: "revision", Spec: revision},
	)
	if err != nil {
		return nil, fmt.Errorf("diff specs: %w", err)
	}

	found := checker.CheckBackwardCompatibilityUntilLevel(
		checker.NewConfig(checker.GetAllChecks()), d, sources, checker.INFO,
	)

	l := checker.NewDefaultLocalizer()
	changes := make([]Change, 0, len(found))
	for _, c := range found {
		var op string
		if c.GetOperation() != "" && c.GetPath() != "" {
			op = strings.ToUpper(c.GetOperation()) + " " + c.GetPath()
		}
		changes = append(changes, Change{
			Level:     fromCheckerLevel(c.GetLevel()),
			ID:        c.GetId(),
			Operation: op,
			Message:   c.GetUncolorizedText(l),
		})
	}
	sortBySeverity(changes)
	return changes, nil
}

// ResolveTools sets Tool on every change whose Operation appears in
// toolByOperation ("METHOD /path" → tool name).
func ResolveTools(changes []Change, toolByOperation map[string]string) {
	for i := range changes {
		if name, ok := toolByOperation[changes[i].Operation]; ok {
			changes[i].Tool = name
		}
	}
}

// MaxLevel returns the highest Level among changes, or LevelNone if empty.
func MaxLevel(changes []Change) Level {
	highest := LevelNone
	for _, c := range changes {
		if c.Level > highest {
			highest = c.Level
		}
	}
	return highest
}

// Counts tallies changes by level.
type Counts struct {
	Err, Warn, Info int
}

// Breaking returns the number of breaking (error + warning) changes.
func (c Counts) Breaking() int {
	return c.Err + c.Warn
}

// Count tallies changes by level.
func Count(changes []Change) Counts {
	var c Counts
	for _, ch := range changes {
		switch ch.Level {
		case LevelErr:
			c.Err++
		case LevelWarn:
			c.Warn++
		case LevelInfo:
			c.Info++
		case LevelNone:
		}
	}
	return c
}

// sortBySeverity stably orders changes most severe first, keeping oasdiff's
// order within a level.
func sortBySeverity(changes []Change) {
	slices.SortStableFunc(changes, func(a, b Change) int { return int(b.Level) - int(a.Level) })
}

// fromCheckerLevel maps oasdiff's level onto Level.
func fromCheckerLevel(l checker.Level) Level {
	switch l {
	case checker.ERR:
		return LevelErr
	case checker.WARN:
		return LevelWarn
	case checker.INFO:
		return LevelInfo
	case checker.NONE, checker.INVALID:
	}
	return LevelNone
}
