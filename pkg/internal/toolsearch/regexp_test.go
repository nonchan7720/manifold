package toolsearch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchRegexp(t *testing.T) {
	docs := []ToolDef{
		{Name: "order_search", Description: "search orders by id"},
		{Name: "user_list", Description: "list all users"},
		{Name: "cancel_order", Description: "cancel an existing order"},
	}

	tests := []struct {
		name    string
		docs    []ToolDef
		query   string
		limit   int
		want    []string
		wantErr bool
	}{
		{
			name:  "matches_name",
			docs:  docs,
			query: "^order_",
			limit: 10,
			want:  []string{"order_search"},
		},
		{
			name:  "matches_description",
			docs:  docs,
			query: "existing",
			limit: 10,
			want:  []string{"cancel_order"},
		},
		{
			name:  "case_insensitive",
			docs:  docs,
			query: "ORDER",
			limit: 10,
			want:  []string{"order_search", "cancel_order"},
		},
		{
			name:  "no_match",
			docs:  docs,
			query: "nonexistent-pattern",
			limit: 10,
			want:  nil,
		},
		{
			name:  "limit_truncates",
			docs:  docs,
			query: "order|user",
			limit: 2,
			want:  []string{"order_search", "user_list"},
		},
		{
			name:    "invalid_pattern_returns_error",
			docs:    docs,
			query:   "(unterminated",
			limit:   10,
			wantErr: true,
		},
		{
			name:  "empty_query_returns_no_results",
			docs:  docs,
			query: "",
			limit: 10,
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := searchRegexp(tt.docs, tt.query, tt.limit)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, names(got))
		})
	}
}

func TestSearchRegexp_MatchesArgumentName(t *testing.T) {
	docs := []ToolDef{
		{
			Name:        "search_records",
			Description: "search records in the system",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"orderId": map[string]any{"type": "string"},
				},
			},
		},
		{Name: "list_users", Description: "list all users"},
	}

	got, err := searchRegexp(docs, "^orderId$", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"search_records"}, names(got))
}

func TestSearchRegexp_MatchesArgumentDescription(t *testing.T) {
	docs := []ToolDef{
		{
			Name:        "search_records",
			Description: "search records",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"orderId": map[string]any{
						"type":        "string",
						"description": "unique order identifier",
					},
				},
			},
		},
		{Name: "list_users", Description: "list all users"},
	}

	got, err := searchRegexp(docs, "identifier", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"search_records"}, names(got))
}
