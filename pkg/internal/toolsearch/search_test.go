package toolsearch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearch_DefaultMethodIsBM25(t *testing.T) {
	docs := []ToolDef{{Name: "order search", Description: "find orders"}}
	got, err := Search(docs, "order", "", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestSearch_MethodIsCaseInsensitive(t *testing.T) {
	docs := []ToolDef{{Name: "order search", Description: "find orders"}}
	for _, method := range []Method{"BM25", "Regexp", "FUZZY"} {
		got, err := Search(docs, "order", method, 10)
		require.NoError(t, err, method)
		require.Len(t, got, 1, method)
	}
}

func TestSearch_UnknownMethodReturnsError(t *testing.T) {
	_, err := Search([]ToolDef{{Name: "order search"}}, "order", Method("unknown"), 10)
	require.Error(t, err)
}

func TestSearch_EmptyDocsReturnsEmpty(t *testing.T) {
	for _, method := range []Method{MethodBM25, MethodRegexp, MethodFuzzy} {
		got, err := Search(nil, "order", method, 10)
		require.NoError(t, err, method)
		require.Empty(t, got, method)
	}
}

func TestIndex_SearchMatchesSearch(t *testing.T) {
	docs := []ToolDef{
		{Name: "list_orders", Description: "list orders"},
		{Name: "get_pet", Description: "get a pet by id"},
	}
	ix := NewIndex(docs)
	require.Equal(t, docs, ix.Docs())
	// 同じ Index への繰り返し検索（前処理の再利用）が Search と同じ結果を返す。
	for range 2 {
		for _, method := range []Method{"", MethodBM25, MethodRegexp, MethodFuzzy} {
			want, err := Search(docs, "pet", method, 10)
			require.NoError(t, err)
			got, err := ix.Search("pet", method, 10)
			require.NoError(t, err)
			require.Equal(t, want, got, method)
		}
	}
	_, err := ix.Search("pet", Method("unknown"), 10)
	require.Error(t, err)
}

func TestDigest_ReturnsAllEntriesSortedByName(t *testing.T) {
	got := Digest([]ToolDef{
		{Name: "zebra", Description: "zebra desc"},
		{Name: "apple", Description: "apple desc"},
		{Name: "mango"},
	})
	require.Equal(t, []DigestEntry{
		{Name: "apple", Description: "apple desc"},
		{Name: "mango", Description: ""},
		{Name: "zebra", Description: "zebra desc"},
	}, got)
}

func TestDigest_Empty_ReturnsNil(t *testing.T) {
	require.Nil(t, Digest(nil))
	require.Nil(t, Digest([]ToolDef{}))
}
