package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

type introspectionStubResponse struct {
	active bool
	sub    string
}

// introspectionTestServer is a stub RFC 7662-shaped introspection endpoint:
// POST token=<credential> -> {"active":bool,"sub":string}. Tests configure
// per-credential responses and can force a failure status to exercise the
// stale-cache/ErrUnavailable paths.
type introspectionTestServer struct {
	srv   *httptest.Server
	calls atomic.Int32

	mu        sync.Mutex
	responses map[string]introspectionStubResponse
	status    int
	delay     time.Duration
}

func newIntrospectionTestServer(t *testing.T) *introspectionTestServer {
	t.Helper()
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要

	s := &introspectionTestServer{
		responses: map[string]introspectionStubResponse{},
		status:    http.StatusOK,
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)

		s.mu.Lock()
		delay := s.delay
		status := s.status
		s.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}

		require.NoError(t, r.ParseForm())
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}

		token := r.PostFormValue("token")
		s.mu.Lock()
		stub, ok := s.responses[token]
		s.mu.Unlock()
		if !ok {
			stub = introspectionStubResponse{active: false}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(introspectionResponse{Active: stub.active, Sub: stub.sub})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *introspectionTestServer) setResponse(credential string, active bool, sub string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[credential] = introspectionStubResponse{active: active, sub: sub}
}

func (s *introspectionTestServer) setStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = code
}

func (s *introspectionTestServer) setDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

func newIntrospectionRequest(credential string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp/app1", nil)
	if credential != "" {
		req.Header.Set("X-Api-Key", credential)
	}
	return req
}

func newIntrospectionProfile(url string, cacheTTL time.Duration) *config.IdentityProfile {
	return &config.IdentityProfile{
		Source:           config.IdentitySourceIntrospection,
		URL:              url,
		CredentialHeader: "X-Api-Key",
		CacheTTL:         cacheTTL,
	}
}

// newTestIntrospectionResolver builds a "rotatingKey" resolver against s.
func newTestIntrospectionResolver(
	t *testing.T,
	s *introspectionTestServer,
	cacheTTL time.Duration,
) Resolver {
	t.Helper()
	r, err := NewResolver(
		t.Context(), "rotatingKey", newIntrospectionProfile(s.srv.URL, cacheTTL), nil,
	)
	require.NoError(t, err)
	return r
}

func TestIntrospectionResolver_Resolve_Active_ReturnsIdentityKey(t *testing.T) {
	s := newIntrospectionTestServer(t)
	s.setResponse("cred-a", true, "user-a")
	r := newTestIntrospectionResolver(t, s, time.Minute)

	key, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
	require.NoError(t, err)
	require.Equal(t, encodeIdentityKey("rotatingKey", "user-a"), key)
}

func TestIntrospectionResolver_Resolve_MissingCredential_ErrUnauthenticated(t *testing.T) {
	s := newIntrospectionTestServer(t)
	r := newTestIntrospectionResolver(t, s, time.Minute)

	_, err := r.Resolve(t.Context(), newIntrospectionRequest(""))
	require.ErrorIs(t, err, ErrUnauthenticated)
	require.Equal(t, int32(0), s.calls.Load(), "must not call the endpoint without a credential")
}

func TestIntrospectionResolver_Resolve_Inactive_ErrUnauthenticated(t *testing.T) {
	s := newIntrospectionTestServer(t)
	s.setResponse("cred-a", false, "")
	r := newTestIntrospectionResolver(t, s, time.Minute)

	_, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestIntrospectionResolver_Resolve_RotatedCredential_SameSub_SameIdentityKey(t *testing.T) {
	s := newIntrospectionTestServer(t)
	s.setResponse("cred-old", true, "user-a")
	s.setResponse("cred-new", true, "user-a")
	r := newTestIntrospectionResolver(t, s, time.Minute)

	keyOld, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-old"))
	require.NoError(t, err)
	keyNew, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-new"))
	require.NoError(t, err)
	require.Equal(
		t,
		keyOld,
		keyNew,
		"different credentials resolving to the same sub must share an identityKey",
	)
}

func TestIntrospectionResolver_Resolve_CachesWithinTTL(t *testing.T) {
	s := newIntrospectionTestServer(t)
	s.setResponse("cred-a", true, "user-a")
	r := newTestIntrospectionResolver(t, s, time.Minute)

	_, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
	require.NoError(t, err)
	_, err = r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
	require.NoError(t, err)
	require.Equal(
		t, int32(1), s.calls.Load(), "a cached credential must not re-hit the endpoint within TTL",
	)
}

func TestIntrospectionResolver_Resolve_TTLExpiry_Refreshes(t *testing.T) {
	s := newIntrospectionTestServer(t)
	s.setResponse("cred-a", true, "user-a")
	r := newTestIntrospectionResolver(t, s, 30*time.Millisecond)

	_, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
	require.NoError(t, err)

	time.Sleep(80 * time.Millisecond)
	s.setResponse("cred-a", true, "user-a-renamed")

	key, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
	require.NoError(t, err)
	require.Equal(t, encodeIdentityKey("rotatingKey", "user-a-renamed"), key)
	require.Equal(t, int32(2), s.calls.Load())
}

func TestIntrospectionResolver_Resolve_Concurrent_SingleflightMergesCalls(t *testing.T) {
	s := newIntrospectionTestServer(t)
	s.setResponse("cred-a", true, "user-a")
	s.setDelay(50 * time.Millisecond)
	r := newTestIntrospectionResolver(t, s, time.Minute)

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
			require.NoError(t, err)
		}()
	}
	wg.Wait()

	require.Equal(
		t, int32(1), s.calls.Load(), "concurrent resolves for the same credential must be merged",
	)
}

func TestIntrospectionResolver_Resolve_EndpointFailure_NoCache_ErrUnavailable(t *testing.T) {
	s := newIntrospectionTestServer(t)
	s.setStatus(http.StatusInternalServerError)
	r := newTestIntrospectionResolver(t, s, time.Minute)

	_, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestIntrospectionResolver_Resolve_EndpointFailure_StaleCache_Continues(t *testing.T) {
	s := newIntrospectionTestServer(t)
	s.setResponse("cred-a", true, "user-a")
	r := newTestIntrospectionResolver(t, s, 30*time.Millisecond)

	key1, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
	require.NoError(t, err)

	time.Sleep(80 * time.Millisecond)
	s.setStatus(http.StatusInternalServerError)

	key2, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
	require.NoError(t, err, "a stale cache entry must be used when the endpoint is down")
	require.Equal(t, key1, key2)
}

func TestIntrospectionResolver_CacheKey_DoesNotStoreRawCredential(t *testing.T) {
	s := newIntrospectionTestServer(t)
	s.setResponse("super-secret-credential", true, "user-a")
	resolver := newTestIntrospectionResolver(t, s, time.Minute)
	r, ok := resolver.(*introspectionResolver)
	require.True(t, ok)

	_, err := r.Resolve(t.Context(), newIntrospectionRequest("super-secret-credential"))
	require.NoError(t, err)

	r.mu.Lock()
	defer r.mu.Unlock()
	for cacheKey := range r.cache {
		require.NotContains(t, cacheKey, "super-secret-credential")
	}
}
