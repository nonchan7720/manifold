package client

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type fakeTokenSource struct {
	token *oauth2.Token
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	return f.token, nil
}

func TestInMemoryRegistry_Get_Miss(t *testing.T) {
	r := &InMemoryRegistry{}
	ts, ok := r.Get("missing")
	require.False(t, ok)
	require.Nil(t, ts)
}

func TestInMemoryRegistry_Get_Hit(t *testing.T) {
	r := &InMemoryRegistry{}
	want := &fakeTokenSource{token: &oauth2.Token{AccessToken: "abc"}}
	require.NoError(t, r.Set("key", want))

	got, ok := r.Get("key")
	require.True(t, ok)
	require.Same(t, oauth2.TokenSource(want), got)
}

func TestInMemoryRegistry_GetOrCreate_CreatesOnMiss(t *testing.T) {
	r := &InMemoryRegistry{}
	want := &fakeTokenSource{token: &oauth2.Token{AccessToken: "created"}}
	created := false

	got := r.GetOrCreate("key", func() oauth2.TokenSource {
		created = true
		return want
	})

	require.True(t, created)
	require.Same(t, oauth2.TokenSource(want), got)

	// 一度作成された後は同じインスタンスが再利用される
	got2 := r.GetOrCreate("key", func() oauth2.TokenSource {
		t.Fatal("create should not be called again for an existing key")
		return nil
	})
	require.Same(t, oauth2.TokenSource(want), got2)
}

// TestInMemoryRegistry_GetOrCreate_ConcurrentSameKey は、同一キーへの同時初回アクセスでも
// 最終的に全ての呼び出し元が同じ TokenSource インスタンスへ収束することを確認する
// （sync.Map.LoadOrStore によるアトミックな get-or-create）。
func TestInMemoryRegistry_GetOrCreate_ConcurrentSameKey(t *testing.T) {
	r := &InMemoryRegistry{}
	const goroutines = 50

	results := make([]oauth2.TokenSource, goroutines)
	var wg sync.WaitGroup
	var startWg sync.WaitGroup
	startWg.Add(1)

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			startWg.Wait()
			results[i] = r.GetOrCreate("shared-key", func() oauth2.TokenSource {
				return &fakeTokenSource{token: &oauth2.Token{AccessToken: "created"}}
			})
		}(i)
	}
	startWg.Done()
	wg.Wait()

	first := results[0]
	require.NotNil(t, first)
	for i, got := range results {
		require.Same(t, first, got, "goroutine %d should observe the same TokenSource instance", i)
	}
}
