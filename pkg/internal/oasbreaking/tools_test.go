package oasbreaking

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupByTool_StatusesAndOrder(t *testing.T) {
	changes := []Change{
		{
			Level: LevelErr, ID: "new-required-request-parameter",
			Operation: "GET /pet/{petId}", Tool: "getpetbyid",
		},
		{
			Level: LevelErr, ID: "api-path-removed-without-deprecation",
			Operation: "POST /upload", Tool: "uploadfile",
		},
		{Level: LevelInfo, ID: "api-version-not-bumped"},
		{
			Level: LevelInfo, ID: "endpoint-added",
			Operation: "DELETE /pet/{petId}", Tool: "deletepet",
		},
		{
			Level: LevelInfo, ID: "response-optional-property-added",
			Operation: "GET /pet/{petId}", Tool: "getpetbyid",
		},
	}
	got := GroupByTool(changes, CatalogDiff{
		Added:   []CatalogTool{{Name: "deletepet", Operation: "DELETE /pet/{petId}"}},
		Removed: []CatalogTool{{Name: "uploadfile", Operation: "POST /upload"}},
	})

	require.Len(t, got.Tools, 3)
	// Errors first, then by name.
	require.Equal(t, "getpetbyid", got.Tools[0].Name)
	require.Equal(t, ToolChanged, got.Tools[0].Status)
	require.Equal(t, LevelErr, got.Tools[0].Level)
	require.Equal(t, "GET /pet/{petId}", got.Tools[0].Operation)
	require.Len(t, got.Tools[0].Changes, 2)

	require.Equal(t, "uploadfile", got.Tools[1].Name)
	require.Equal(t, ToolRemoved, got.Tools[1].Status)
	require.Equal(t, LevelErr, got.Tools[1].Level)

	require.Equal(t, "deletepet", got.Tools[2].Name)
	require.Equal(t, ToolAdded, got.Tools[2].Status)
	require.Equal(t, LevelInfo, got.Tools[2].Level)
	require.Empty(t, got.Tools[2].Note)

	require.Equal(t, []Change{changes[2]}, got.SpecWide)
	require.Equal(t, LevelErr, got.MaxLevel())
}

func TestGroupByTool_CatalogOnly(t *testing.T) {
	got := GroupByTool(nil, CatalogDiff{
		Added:         []CatalogTool{{Name: "newtool", Operation: "GET /new"}},
		Removed:       []CatalogTool{{Name: "oldtool", Operation: "GET /old"}},
		SchemaChanged: []CatalogTool{{Name: "listpets", Operation: "GET /pet"}},
	})

	require.Len(t, got.Tools, 3)
	// A removed tool is an error even when oasdiff reported nothing for it.
	require.Equal(t, "oldtool", got.Tools[0].Name)
	require.Equal(t, ToolRemoved, got.Tools[0].Status)
	require.Equal(t, LevelErr, got.Tools[0].Level)
	require.Equal(t, noteRemovedOnly, got.Tools[0].Note)

	require.Equal(t, "listpets", got.Tools[1].Name)
	require.Equal(t, ToolChanged, got.Tools[1].Status)
	require.Equal(t, LevelNone, got.Tools[1].Level)
	require.Equal(t, noteSchemaOnly, got.Tools[1].Note)

	require.Equal(t, "newtool", got.Tools[2].Name)
	require.Equal(t, ToolAdded, got.Tools[2].Status)
	require.Equal(t, noteAddedOnly, got.Tools[2].Note)

	require.Empty(t, got.SpecWide)
	require.Equal(t, LevelErr, got.MaxLevel())
}

func TestGroupByTool_NoChanges(t *testing.T) {
	got := GroupByTool(nil, CatalogDiff{})
	require.Empty(t, got.Tools)
	require.Empty(t, got.SpecWide)
	require.Equal(t, LevelNone, got.MaxLevel())
}
