package toolsearch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchFuzzy(t *testing.T) {
	docs := []ToolDef{
		{Name: "order_search", Description: "search orders by id"},
		{Name: "user_list", Description: "list all users"},
		{Name: "cancel_order", Description: "cancel an existing order"},
	}

	tests := []struct {
		name  string
		docs  []ToolDef
		query string
		limit int
		want  []string
	}{
		{
			name:  "exact_substring_matches",
			docs:  docs,
			query: "order_search",
			limit: 10,
			want:  []string{"order_search"},
		},
		{
			name:  "fuzzy_subsequence_match",
			docs:  docs,
			query: "usrlst",
			limit: 10,
			want:  []string{"user_list"},
		},
		{
			name:  "no_match",
			docs:  docs,
			query: "zzzzqqqq",
			limit: 10,
			want:  nil,
		},
		{
			name:  "limit_truncates",
			docs:  docs,
			query: "order",
			limit: 1,
			want:  []string{"order_search"},
		},
		{
			name:  "empty_docs",
			docs:  nil,
			query: "order",
			limit: 10,
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchFuzzy(tt.docs, tt.query, tt.limit)
			require.Equal(t, tt.want, names(got))
			if tt.limit > 0 {
				require.LessOrEqual(t, len(got), tt.limit)
			}
		})
	}
}

func TestSearchFuzzy_MatchesArgumentName(t *testing.T) {
	docs := []ToolDef{
		{
			Name:        "order_search",
			Description: "search orders by id",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					// クエリのサブシーケンスは引数名 "customerRef" にしか出現しない
					"customerRef": map[string]any{"type": "string"},
				},
			},
		},
		{Name: "list_users", Description: "list all users"},
		{Name: "cancel_order", Description: "cancel an existing order"},
	}

	got := searchFuzzy(docs, "cstmrref", 10)
	require.Equal(t, []string{"order_search"}, names(got))
}
