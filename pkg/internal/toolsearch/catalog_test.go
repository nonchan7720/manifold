package toolsearch

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalog_Total_AggregatesAcrossServers(t *testing.T) {
	c := NewCatalog()
	c.Add("serverA", ToolDef{Name: "a1"}, ToolDef{Name: "a2"})
	c.Add("serverB", ToolDef{Name: "b1"})

	require.Equal(t, 3, c.Total())
}

func TestCatalog_Add_SameNameReplacesNotDouble(t *testing.T) {
	c := NewCatalog()
	c.Add("serverA", ToolDef{Name: "tool1", Description: "v1"})
	require.Equal(t, 1, c.Total())

	c.Add("serverA", ToolDef{Name: "tool1", Description: "v2"})
	require.Equal(t, 1, c.Total())

	docs, err := c.Search("serverA", "tool1", MethodRegexp, 10)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "v2", docs[0].Description)
}

func TestCatalog_Search_ScopedToServer(t *testing.T) {
	c := NewCatalog()
	c.Add("serverA", ToolDef{Name: "order_search", Description: "search orders"})
	c.Add("serverB", ToolDef{Name: "order_cancel", Description: "cancel orders"})

	docsA, err := c.Search("serverA", "order", MethodRegexp, 10)
	require.NoError(t, err)
	require.Len(t, docsA, 1)
	require.Equal(t, "order_search", docsA[0].Name)

	docsB, err := c.Search("serverB", "order", MethodRegexp, 10)
	require.NoError(t, err)
	require.Len(t, docsB, 1)
	require.Equal(t, "order_cancel", docsB[0].Name)
}

func TestCatalog_Search_DefaultMethodIsBM25(t *testing.T) {
	c := NewCatalog()
	c.Add("serverA", ToolDef{Name: "order search", Description: "find orders"})

	docs, err := c.Search("serverA", "order", "", 10)
	require.NoError(t, err)
	require.Len(t, docs, 1)
}

func TestCatalog_Search_UnknownMethodReturnsError(t *testing.T) {
	c := NewCatalog()
	c.Add("serverA", ToolDef{Name: "order search", Description: "find orders"})

	_, err := c.Search("serverA", "order", Method("unknown"), 10)
	require.Error(t, err)
}

func TestCatalog_Search_UnknownServerReturnsEmpty(t *testing.T) {
	c := NewCatalog()
	docs, err := c.Search("nonexistent", "order", MethodBM25, 10)
	require.NoError(t, err)
	require.Empty(t, docs)
}

func TestCatalog_Digest_UnknownServer_ReturnsEmpty(t *testing.T) {
	c := NewCatalog()
	require.Empty(t, c.Digest("nonexistent"))
}

func TestCatalog_Digest_ReturnsAllEntriesSortedByName(t *testing.T) {
	c := NewCatalog()
	c.Add("serverA",
		ToolDef{Name: "zebra", Description: "zebra desc"},
		ToolDef{Name: "apple", Description: "apple desc"},
		ToolDef{Name: "mango", Description: "mango desc"},
	)

	got := c.Digest("serverA")
	require.Equal(t, []DigestEntry{
		{Name: "apple", Description: "apple desc"},
		{Name: "mango", Description: "mango desc"},
		{Name: "zebra", Description: "zebra desc"},
	}, got)
}

func TestCatalog_Digest_IncludesDescription(t *testing.T) {
	c := NewCatalog()
	c.Add("serverA", ToolDef{Name: "addPet", Description: "Add a new pet to the store"})

	got := c.Digest("serverA")
	require.Equal(t, []DigestEntry{{Name: "addPet", Description: "Add a new pet to the store"}}, got)
}

func TestCatalog_Digest_NoDescription_ReturnsEmptyDescriptionField(t *testing.T) {
	c := NewCatalog()
	c.Add("serverA", ToolDef{Name: "addPet"})

	got := c.Digest("serverA")
	require.Equal(t, []DigestEntry{{Name: "addPet", Description: ""}}, got)
}

func TestCatalog_Digest_ScopedToServer(t *testing.T) {
	c := NewCatalog()
	c.Add("serverA", ToolDef{Name: "a1", Description: "d1"}, ToolDef{Name: "a2", Description: "d2"})
	c.Add("serverB", ToolDef{Name: "b1", Description: "d3"})

	got := c.Digest("serverA")
	require.Equal(t, []DigestEntry{
		{Name: "a1", Description: "d1"},
		{Name: "a2", Description: "d2"},
	}, got)
}

func TestCatalog_ConcurrentAddAndSearch(t *testing.T) {
	c := NewCatalog()
	var wg sync.WaitGroup

	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.Add("serverA", ToolDef{Name: "tool", Description: "concurrent add"})
		}()
		go func() {
			defer wg.Done()
			_, _ = c.Search("serverA", "tool", MethodBM25, 10)
			_ = c.Total()
		}()
	}
	wg.Wait()
}
