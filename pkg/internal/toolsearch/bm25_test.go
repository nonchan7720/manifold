package toolsearch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func names(docs []ToolDef) []string {
	if len(docs) == 0 {
		return nil
	}
	names := make([]string, len(docs))
	for i, d := range docs {
		names[i] = d.Name
	}
	return names
}

func TestSearchBM25(t *testing.T) {
	tests := []struct {
		name  string
		docs  []ToolDef
		query string
		limit int
		want  []string
	}{
		{
			name: "ranks_more_occurrences_higher",
			docs: []ToolDef{
				{Name: "order search", Description: "find orders by criteria"},
				{Name: "user list", Description: "list all users in system"},
				{Name: "cancel order", Description: "cancel an existing order"},
			},
			query: "order",
			limit: 10,
			want:  []string{"cancel order", "order search"},
		},
		{
			name: "no_match_returns_empty",
			docs: []ToolDef{
				{Name: "user list", Description: "list all users in system"},
			},
			query: "order",
			limit: 10,
			want:  nil,
		},
		{
			name: "tie_break_by_name_ascending",
			docs: []ToolDef{
				{Name: "bravo widget", Description: "widget for testing"},
				{Name: "alpha widget", Description: "widget for testing"},
			},
			query: "widget",
			limit: 10,
			want:  []string{"alpha widget", "bravo widget"},
		},
		{
			name: "limit_truncates_results",
			docs: []ToolDef{
				{Name: "widget one", Description: "widget"},
				{Name: "widget two", Description: "widget"},
				{Name: "widget three", Description: "widget"},
			},
			query: "widget",
			limit: 2,
			want:  []string{"widget one", "widget three"},
		},
		{
			name:  "empty_docs",
			docs:  nil,
			query: "widget",
			limit: 10,
			want:  nil,
		},
		{
			name: "empty_query",
			docs: []ToolDef{
				{Name: "widget one", Description: "widget"},
			},
			query: "",
			limit: 10,
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchBM25(tt.docs, tt.query, tt.limit)
			require.Equal(t, tt.want, names(got))
			if tt.limit > 0 {
				require.LessOrEqual(t, len(got), tt.limit)
			}
		})
	}
}

func TestSearchBM25_MatchesArgumentName(t *testing.T) {
	docs := []ToolDef{
		{
			Name:        "search_records",
			Description: "search records in the system",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"orderId": map[string]any{
						"type": "string",
						// クエリ語は引数名にしか出現しない
					},
				},
			},
		},
		{
			Name:        "list_users",
			Description: "list all users",
		},
	}

	got := searchBM25(docs, "orderId", 10)
	require.Equal(t, []string{"search_records"}, names(got))
}

func TestSearchBM25_MatchesArgumentDescription_CJK(t *testing.T) {
	docs := []ToolDef{
		{
			Name:        "search_records",
			Description: "search records",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"orderId": map[string]any{
						"type":        "string",
						"description": "注文番号で検索する",
					},
				},
			},
		},
		{
			Name:        "list_users",
			Description: "list all users",
		},
	}

	got := searchBM25(docs, "注文番号", 10)
	require.Equal(t, []string{"search_records"}, names(got))
}
