package client

import (
	"sync"

	"golang.org/x/oauth2"
)

type InMemoryRegistry struct {
	sources sync.Map
}

func (r *InMemoryRegistry) Get(apiKey string) (oauth2.TokenSource, bool) {
	v, ok := r.sources.Load(apiKey)
	if ok {
		return nil, false
	}
	if ts, ok := v.(oauth2.TokenSource); ok {
		return ts, true
	}
	return nil, false
}

func (r *InMemoryRegistry) Set(apiKey string, ts oauth2.TokenSource) error {
	r.sources.Store(apiKey, ts)
	return nil
}
