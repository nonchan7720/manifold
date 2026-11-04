package client

import (
	"sync"

	"golang.org/x/oauth2"
)

// InMemoryBaseTokenRegister は BaseTokenRegister のプロセス内メモリ実装。token exchange の
// apiKey ごとに、交換済みトークンをキャッシュする oauth2.TokenSource を保持する。
type InMemoryBaseTokenRegister struct {
	sources sync.Map
}

var _ BaseTokenRegister = (*InMemoryBaseTokenRegister)(nil)

func (r *InMemoryBaseTokenRegister) Get(apiKey string) (oauth2.TokenSource, bool) {
	v, ok := r.sources.Load(apiKey)
	if !ok {
		return nil, false
	}
	if ts, ok := v.(oauth2.TokenSource); ok {
		return ts, true
	}
	return nil, false
}

func (r *InMemoryBaseTokenRegister) Set(apiKey string, ts oauth2.TokenSource) error {
	r.sources.Store(apiKey, ts)
	return nil
}

// GetOrCreate は apiKey に対応する TokenSource を取得する。存在しない場合のみ create で
// 新規作成し、sync.Map.LoadOrStore によって取得と格納をアトミックに行う。これにより、
// 同一 apiKey に対して複数の goroutine が同時に初回アクセスした場合でも、最終的に
// 全ての呼び出し元が同じ TokenSource インスタンスを共有することを保証する
// （create が複数回呼ばれること自体はあり得るが、実際に保存・返却されるのは常に1つだけ）。
func (r *InMemoryBaseTokenRegister) GetOrCreate(
	apiKey string,
	create func() oauth2.TokenSource,
) oauth2.TokenSource {
	if v, ok := r.sources.Load(apiKey); ok {
		if ts, ok := v.(oauth2.TokenSource); ok {
			return ts
		}
	}
	ts := create()
	actual, loaded := r.sources.LoadOrStore(apiKey, ts)
	if loaded {
		if existing, ok := actual.(oauth2.TokenSource); ok {
			return existing
		}
	}
	return ts
}
