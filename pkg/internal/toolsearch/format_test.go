package toolsearch

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatResults_Default_ReturnsToolDefs(t *testing.T) {
	defs := []ToolDef{
		{Name: "tool_a", Description: "desc a"},
		{Name: "tool_b", Description: "desc b"},
	}

	tests := []struct {
		name   string
		format ResultFormat
	}{
		{"explicit_default", ResultFormatDefault},
		{"empty_string_treated_as_default", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatResults(tt.format, defs)
			require.NoError(t, err)
			require.Equal(t, defs, got)
		})
	}
}

func TestFormatResults_Claude_ReturnsToolReferences(t *testing.T) {
	defs := []ToolDef{
		{Name: "tool_a", Description: "desc a"},
		{Name: "tool_b", Description: "desc b"},
	}

	got, err := FormatResults(ResultFormatClaude, defs)
	require.NoError(t, err)
	require.Equal(t, []ToolReference{
		{Type: "tool_reference", ToolName: "tool_a"},
		{Type: "tool_reference", ToolName: "tool_b"},
	}, got)
}

func TestFormatResults_UnknownFormat_Error(t *testing.T) {
	_, err := FormatResults(ResultFormat("bogus"), []ToolDef{{Name: "tool_a"}})
	require.Error(t, err)
}

func TestFormatResults_EmptyDefs_ReturnsEmptySlice(t *testing.T) {
	tests := []struct {
		name   string
		format ResultFormat
	}{
		{"default_nil_defs", ResultFormatDefault},
		{"claude_nil_defs", ResultFormatClaude},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatResults(tt.format, nil)
			require.NoError(t, err)

			data, err := json.Marshal(got)
			require.NoError(t, err)
			require.JSONEq(t, "[]", string(data))
		})
	}
}

func TestToolReference_JSONShape(t *testing.T) {
	ref := ToolReference{Type: "tool_reference", ToolName: "search_orders"}
	data, err := json.Marshal(ref)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"tool_reference","tool_name":"search_orders"}`, string(data))
}
