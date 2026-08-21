package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/internal/client"
	"golang.org/x/sync/singleflight"
)

// inactiveCredentialCacheTTL bounds how long an "active: false" result is
// cached, independent of the profile's cacheTTL — it must never be
// remembered as long as a successful resolution.
const inactiveCredentialCacheTTL = 30 * time.Second

type introspectionCacheEntry struct {
	active    bool
	sub       string
	expiresAt time.Time
}

type introspectionResponse struct {
	Active bool   `json:"active"`
	Sub    string `json:"sub"`
}

// introspectionResolver derives an identityKey by exchanging the
// credentialHeader value for a stable sub via an RFC 7662-shaped endpoint,
// with a TTL cache (keyed by a hash of the credential, never the credential
// itself) and singleflight-merged lookups for concurrent identical
// credentials.
type introspectionResolver struct {
	profile          string
	credentialHeader string
	url              string
	cacheTTL         time.Duration
	httpClient       *http.Client

	mu    sync.Mutex
	cache map[string]introspectionCacheEntry

	group singleflight.Group
}

func newIntrospectionResolver(
	profileName string,
	p *config.IdentityProfile,
) *introspectionResolver {
	return &introspectionResolver{
		profile:          profileName,
		credentialHeader: p.CredentialHeader,
		url:              p.URL,
		cacheTTL:         p.CacheTTLOrDefault(),
		httpClient:       client.HTTPClient(),
		cache:            make(map[string]introspectionCacheEntry),
	}
}

func (r *introspectionResolver) Resolve(
	ctx context.Context,
	req *http.Request,
) (domainedge.IdentityKey, error) {
	credential := req.Header.Get(r.credentialHeader)
	if credential == "" {
		return "", ErrUnauthenticated
	}
	cacheKey := r.cacheKey(credential)

	if entry, ok := r.lookupFresh(cacheKey); ok {
		return r.result(entry)
	}

	v, err, _ := r.group.Do(cacheKey, func() (any, error) {
		return r.refresh(ctx, cacheKey, credential)
	})
	if err != nil {
		return "", err
	}
	entry, ok := v.(introspectionCacheEntry)
	if !ok {
		return "", fmt.Errorf("identity: unexpected singleflight result type %T", v)
	}
	return r.result(entry)
}

func (r *introspectionResolver) result(
	entry introspectionCacheEntry,
) (domainedge.IdentityKey, error) {
	if !entry.active {
		return "", ErrUnauthenticated
	}
	if entry.sub == "" {
		return "", fmt.Errorf("%w: introspection response missing sub", ErrUnauthenticated)
	}
	return encodeIdentityKey(r.profile, entry.sub), nil
}

// refresh calls the introspection endpoint and caches the result. On
// endpoint failure it falls back to a stale cache entry (any age) so a
// transient outage doesn't interrupt already-authenticated callers; only
// when no cache entry exists at all does it return ErrUnavailable.
func (r *introspectionResolver) refresh(
	ctx context.Context,
	cacheKey, credential string,
) (introspectionCacheEntry, error) {
	resp, err := r.introspect(ctx, credential)
	if err != nil {
		if stale, ok := r.lookupStale(cacheKey); ok {
			return stale, nil
		}
		return introspectionCacheEntry{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	ttl := r.cacheTTL
	if !resp.Active {
		ttl = min(ttl, inactiveCredentialCacheTTL)
	}
	entry := introspectionCacheEntry{
		active: resp.Active, sub: resp.Sub, expiresAt: time.Now().Add(ttl),
	}
	r.store(cacheKey, entry)
	return entry, nil
}

func (r *introspectionResolver) introspect(
	ctx context.Context,
	credential string,
) (introspectionResponse, error) {
	form := url.Values{"token": {credential}}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, r.url, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return introspectionResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return introspectionResponse{}, err
	}
	defer resp.Body.Close() //nolint: errcheck

	if resp.StatusCode != http.StatusOK {
		return introspectionResponse{}, fmt.Errorf(
			"introspection endpoint %q returned status %d", r.url, resp.StatusCode,
		)
	}
	var out introspectionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return introspectionResponse{}, fmt.Errorf("decode introspection response: %w", err)
	}
	return out, nil
}

func (r *introspectionResolver) lookupFresh(cacheKey string) (introspectionCacheEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[cacheKey]
	if !ok || time.Now().After(entry.expiresAt) {
		return introspectionCacheEntry{}, false
	}
	return entry, true
}

func (r *introspectionResolver) lookupStale(cacheKey string) (introspectionCacheEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[cacheKey]
	return entry, ok
}

func (r *introspectionResolver) store(cacheKey string, entry introspectionCacheEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[cacheKey] = entry
}

func (r *introspectionResolver) cacheKey(credential string) string {
	sum := sha256.Sum256([]byte(r.profile + ":" + credential))
	return hex.EncodeToString(sum[:])
}
