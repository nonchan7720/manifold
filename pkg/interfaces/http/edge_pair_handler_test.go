package httphandler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
	"github.com/stretchr/testify/require"
)

// failingSetStore wraps a store.Client and fails Set for keys matching
// keyPrefix, simulating a backend outage isolated to one write path (e.g.
// RateLimitPairAttempt's own counter, edge:pair_ratelimit: — see pairing.go's
// ipRateLimitKeyPrefix) without disturbing unrelated Set calls such as
// IssueCode's.
type failingSetStore struct {
	store.Client
	keyPrefix string
	err       error
}

func (s *failingSetStore) Set(
	ctx context.Context, key string, value any, expiration time.Duration,
) error {
	if strings.HasPrefix(key, s.keyPrefix) {
		return s.err
	}
	return s.Client.Set(ctx, key, value, expiration)
}

// failingGetStore wraps a store.Client and fails Get for keys matching
// keyPrefix with a non-store.ErrNotFound error, simulating a backend outage
// isolated to one key space (e.g. the pairing code itself,
// edge:pairing_code: — see pairing.go's pairingCodeKeyPrefix).
type failingGetStore struct {
	store.Client
	keyPrefix string
	err       error
}

func (s *failingGetStore) Get(ctx context.Context, key string) (string, error) {
	if strings.HasPrefix(key, s.keyPrefix) {
		return "", s.err
	}
	return s.Client.Get(ctx, key)
}

func newTestEdgePairHandler(t *testing.T) (*EdgePairHandler, *edgeservices.PairingService) {
	t.Helper()
	return newTestEdgePairHandlerWithConfig(t, config.EdgeConfig{})
}

func newTestEdgePairHandlerWithConfig(
	t *testing.T,
	edgeCfg config.EdgeConfig,
) (*EdgePairHandler, *edgeservices.PairingService) {
	t.Helper()
	storeClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeClient.Close() })
	pairing := edgeservices.NewPairingService(storeClient)
	return NewEdgePairHandler(pairing, edgeCfg), pairing
}

func TestEdgePairHandler_Pair_ValidCode_ReturnsToken(t *testing.T) {
	handler, pairing := newTestEdgePairHandler(t)
	code, err := pairing.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{"code": code})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/edge/pair",
		bytes.NewReader(body),
	)
	rec := httptest.NewRecorder()

	handler.Pair(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotEmpty(t, resp.Token)
}

func TestEdgePairHandler_Pair_WithExistingToken_AppendsBinding(t *testing.T) {
	handler, pairing := newTestEdgePairHandler(t)

	firstCode, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("oauth:user-a"))
	require.NoError(t, err)
	firstBody, _ := json.Marshal(map[string]string{"code": firstCode})
	firstReq := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/edge/pair",
		bytes.NewReader(firstBody),
	)
	firstRec := httptest.NewRecorder()
	handler.Pair(firstRec, firstReq)
	require.Equal(t, http.StatusOK, firstRec.Code)
	var firstResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(firstRec.Body).Decode(&firstResp))

	secondCode, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("saml:user-a"))
	require.NoError(t, err)
	secondBody, _ := json.Marshal(map[string]string{"code": secondCode, "token": firstResp.Token})
	secondReq := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/edge/pair",
		bytes.NewReader(secondBody),
	)
	secondRec := httptest.NewRecorder()
	handler.Pair(secondRec, secondReq)
	require.Equal(t, http.StatusOK, secondRec.Code)
	var secondResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(secondRec.Body).Decode(&secondResp))
	require.Equal(t, firstResp.Token, secondResp.Token, "appending must return the same edge token")

	keys, err := pairing.Authenticate(t.Context(), firstResp.Token)
	require.NoError(t, err)
	require.ElementsMatch(t, []domainedge.IdentityKey{"oauth:user-a", "saml:user-a"}, keys)
}

func TestEdgePairHandler_Pair_InvalidCode_BadRequest(t *testing.T) {
	handler, _ := newTestEdgePairHandler(t)

	body, _ := json.Marshal(map[string]string{"code": "00000000"})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/edge/pair",
		bytes.NewReader(body),
	)
	rec := httptest.NewRecorder()

	handler.Pair(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestEdgePairHandler_Pair_MalformedBody_BadRequest(t *testing.T) {
	handler, _ := newTestEdgePairHandler(t)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/edge/pair",
		bytes.NewReader([]byte("not json")),
	)
	rec := httptest.NewRecorder()

	handler.Pair(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestEdgePairHandler_Pair_EmptyCode_BadRequest(t *testing.T) {
	handler, _ := newTestEdgePairHandler(t)

	body, _ := json.Marshal(map[string]string{"code": ""})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/edge/pair",
		bytes.NewReader(body),
	)
	rec := httptest.NewRecorder()

	handler.Pair(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- rate limiting (総当たり対策) ---

func newPairRequest(t *testing.T, remoteAddr string, code string) *http.Request {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/edge/pair",
		bytes.NewReader(body),
	)
	req.RemoteAddr = remoteAddr
	return req
}

func TestEdgePairHandler_Pair_RateLimit_AllowsUpToTenPerIP(t *testing.T) {
	handler, pairing := newTestEdgePairHandler(t)

	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		handler.Pair(rec, newPairRequest(t, "203.0.113.5:1234", code))
		require.Equal(t, http.StatusOK, rec.Code, "attempt %d", i+1)
	}
}

func TestEdgePairHandler_Pair_RateLimit_EleventhIsTooManyRequests(t *testing.T) {
	handler, pairing := newTestEdgePairHandler(t)

	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		handler.Pair(rec, newPairRequest(t, "203.0.113.6:1234", code))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	rec := httptest.NewRecorder()
	handler.Pair(rec, newPairRequest(t, "203.0.113.6:1234", "00000000"))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestEdgePairHandler_Pair_RateLimit_IndependentPerIP(t *testing.T) {
	handler, pairing := newTestEdgePairHandler(t)

	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		handler.Pair(rec, newPairRequest(t, "203.0.113.7:1234", code))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("other-ip-user"))
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	handler.Pair(rec, newPairRequest(t, "203.0.113.8:1234", code))
	require.Equal(t, http.StatusOK, rec.Code, "rate limit must not leak across IPs")
}

func TestEdgePairHandler_Pair_ExchangeCodeStoreError_InternalServerError(t *testing.T) {
	memClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = memClient.Close() })
	failing := &failingGetStore{
		Client:    memClient,
		keyPrefix: "edge:pairing_code:",
		err:       errors.New("store unavailable"),
	}
	pairing := edgeservices.NewPairingService(failing)
	handler := NewEdgePairHandler(pairing, config.EdgeConfig{})

	rec := httptest.NewRecorder()
	handler.Pair(rec, newPairRequest(t, "203.0.113.51:1234", "00000000"))
	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"a store failure resolving the pairing code itself must not be reported as invalid_code")
}

func TestEdgePairHandler_Pair_RateLimitStoreError_InternalServerError(t *testing.T) {
	memClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = memClient.Close() })
	failing := &failingSetStore{
		Client:    memClient,
		keyPrefix: "edge:pair_ratelimit:",
		err:       errors.New("store unavailable"),
	}
	pairing := edgeservices.NewPairingService(failing)
	handler := NewEdgePairHandler(pairing, config.EdgeConfig{})

	code, err := pairing.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	handler.Pair(rec, newPairRequest(t, "203.0.113.50:1234", code))
	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"a store failure resolving the rate-limit counter must not be reported as rate_limited")
}

func TestEdgePairHandler_Pair_RateLimit_RemoteAddrWithoutPort_KeyedPerAddress(t *testing.T) {
	handler, pairing := newTestEdgePairHandler(t)

	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		handler.Pair(rec, newPairRequest(t, "203.0.113.77", code))
		require.Equal(t, http.StatusOK, rec.Code, "attempt %d", i+1)
	}
	rec := httptest.NewRecorder()
	handler.Pair(rec, newPairRequest(t, "203.0.113.77", "00000000"))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("other"))
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	handler.Pair(rec, newPairRequest(t, "203.0.113.78", code))
	require.Equal(t, http.StatusOK, rec.Code,
		"a port-less RemoteAddr must be keyed by its own address, not collapsed into an empty key")
}

// --- trusted forwarders (Cloudflare / custom CIDR opt-in) ---

func TestEdgePairHandler_Pair_RateLimit_UntrustedRemoteAddrIgnoresForwardedHeader(t *testing.T) {
	handler, pairing := newTestEdgePairHandler(t)

	// Each attempt claims a distinct X-Forwarded-For; if that header were
	// honored for an untrusted RemoteAddr it would bypass the rate limit by
	// spreading attempts across "different" IPs.
	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		req := newPairRequest(t, "203.0.113.9:1234", code)
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		rec := httptest.NewRecorder()
		handler.Pair(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "attempt %d", i+1)
	}

	code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("eleventh"))
	require.NoError(t, err)
	req := newPairRequest(t, "203.0.113.9:1234", code)
	req.Header.Set("X-Forwarded-For", "198.51.100.99")
	rec := httptest.NewRecorder()
	handler.Pair(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"an untrusted RemoteAddr must not let a spoofed X-Forwarded-For bypass its own rate limit")
}

func TestEdgePairHandler_Pair_RateLimit_XForwardedForMultiValue_UsesRightmostUntrusted(
	t *testing.T,
) {
	handler, pairing := newTestEdgePairHandler(t)

	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		req := newPairRequest(t, fmt.Sprintf("10.0.0.%d:1234", i+1), code)
		req.Header.Set("X-Forwarded-For", "198.51.100.42, 10.0.0.200")
		rec := httptest.NewRecorder()
		handler.Pair(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "attempt %d", i+1)
	}

	code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("eleventh"))
	require.NoError(t, err)
	req := newPairRequest(t, "10.0.0.11:1234", code)
	req.Header.Set("X-Forwarded-For", "198.51.100.42, 10.0.0.200")
	rec := httptest.NewRecorder()
	handler.Pair(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"10.0.0.200 is a trusted hop and must be skipped; 198.51.100.42 is the client")

	otherCode, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("other-client"))
	require.NoError(t, err)
	otherReq := newPairRequest(t, "10.0.0.12:1234", otherCode)
	otherReq.Header.Set("X-Forwarded-For", "198.51.100.43, 10.0.0.200")
	otherRec := httptest.NewRecorder()
	handler.Pair(otherRec, otherReq)
	require.Equal(t, http.StatusOK, otherRec.Code,
		"a different leftmost client behind the same trusted hop gets its own bucket")
}

func TestEdgePairHandler_Pair_RateLimit_TrustCloudflareReadsCFConnectingIP(t *testing.T) {
	handler, pairing := newTestEdgePairHandlerWithConfig(
		t, config.EdgeConfig{TrustCloudflare: true},
	)

	// Each attempt arrives from a different Cloudflare edge IP (all within
	// the published range 173.245.48.0/20); only a shared CF-Connecting-IP
	// ties them into the same rate-limit bucket.
	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		req := newPairRequest(t, fmt.Sprintf("173.245.48.%d:1234", i+1), code)
		req.Header.Set("CF-Connecting-IP", "198.51.100.42")
		rec := httptest.NewRecorder()
		handler.Pair(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "attempt %d", i+1)
	}

	code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("eleventh"))
	require.NoError(t, err)
	req := newPairRequest(t, "173.245.48.11:1234", code)
	req.Header.Set("CF-Connecting-IP", "198.51.100.42")
	rec := httptest.NewRecorder()
	handler.Pair(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"every attempt came from a different Cloudflare edge; only CF-Connecting-IP "+
			"ties them together")
}

func TestEdgePairHandler_Pair_RateLimit_TrustCloudflareFalseIgnoresCFConnectingIP(t *testing.T) {
	handler, pairing := newTestEdgePairHandler(t)

	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		req := newPairRequest(t, "173.245.48.1:1234", code)
		req.Header.Set("CF-Connecting-IP", fmt.Sprintf("198.51.100.%d", i+1))
		rec := httptest.NewRecorder()
		handler.Pair(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "attempt %d", i+1)
	}

	code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("eleventh"))
	require.NoError(t, err)
	req := newPairRequest(t, "173.245.48.1:1234", code)
	req.Header.Set("CF-Connecting-IP", "198.51.100.99")
	rec := httptest.NewRecorder()
	handler.Pair(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"a different CF-Connecting-IP on every attempt must not open a new bucket "+
			"when Cloudflare isn't trusted")
}

func TestEdgePairHandler_Pair_RateLimit_TrustedForwardersReadsHeaderFromCustomCIDR(t *testing.T) {
	handler, pairing := newTestEdgePairHandlerWithConfig(t, config.EdgeConfig{
		TrustedForwarders: []string{"192.0.2.0/24"},
	})

	// Each attempt arrives from a different address within the trusted CIDR;
	// only a shared X-Forwarded-For ties them into the same rate-limit
	// bucket.
	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		req := newPairRequest(t, fmt.Sprintf("192.0.2.%d:1234", i+1), code)
		req.Header.Set("X-Forwarded-For", "198.51.100.42")
		rec := httptest.NewRecorder()
		handler.Pair(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "attempt %d", i+1)
	}

	code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("eleventh"))
	require.NoError(t, err)
	req := newPairRequest(t, "192.0.2.11:1234", code)
	req.Header.Set("X-Forwarded-For", "198.51.100.42")
	rec := httptest.NewRecorder()
	handler.Pair(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"every attempt came from a different address in the trusted CIDR; only "+
			"X-Forwarded-For ties them together")
}

func TestEdgePairHandler_Pair_RateLimit_TrustedForwardersIgnoresSpoofedCFConnectingIP(
	t *testing.T,
) {
	handler, pairing := newTestEdgePairHandlerWithConfig(t, config.EdgeConfig{
		TrustedForwarders: []string{"192.0.2.0/24"},
	})

	// Each request claims a distinct CF-Connecting-IP; if that header were
	// honored for a trustedForwarders-origin connection it would bypass the
	// rate limit by spreading attempts across "different" IPs.
	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		req := newPairRequest(t, "192.0.2.1:1234", code)
		req.Header.Set("CF-Connecting-IP", fmt.Sprintf("198.51.100.%d", i))
		rec := httptest.NewRecorder()
		handler.Pair(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "attempt %d", i+1)
	}

	code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("eleventh"))
	require.NoError(t, err)
	req := newPairRequest(t, "192.0.2.1:1234", code)
	req.Header.Set("CF-Connecting-IP", "198.51.100.99")
	rec := httptest.NewRecorder()
	handler.Pair(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"a spoofed CF-Connecting-IP from a trustedForwarders-origin connection must not "+
			"create separate rate-limit buckets")
}

func TestEdgePairHandler_Pair_RateLimit_TrustCloudflareIgnoresSpoofedXForwardedFor(t *testing.T) {
	handler, pairing := newTestEdgePairHandlerWithConfig(
		t, config.EdgeConfig{TrustCloudflare: true},
	)

	// 173.245.48.1 falls inside Cloudflare's published range 173.245.48.0/20.
	// Each request claims a distinct X-Forwarded-For; if that header were
	// honored for a Cloudflare-origin connection it would bypass the rate
	// limit by spreading attempts across "different" IPs.
	for i := range 10 {
		code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey(fmt.Sprintf("u-%d", i)))
		require.NoError(t, err)
		req := newPairRequest(t, "173.245.48.1:1234", code)
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		rec := httptest.NewRecorder()
		handler.Pair(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "attempt %d", i+1)
	}

	code, err := pairing.IssueCode(t.Context(), domainedge.IdentityKey("eleventh"))
	require.NoError(t, err)
	req := newPairRequest(t, "173.245.48.1:1234", code)
	req.Header.Set("X-Forwarded-For", "198.51.100.99")
	rec := httptest.NewRecorder()
	handler.Pair(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"a spoofed X-Forwarded-For from a Cloudflare-origin connection must not "+
			"create separate rate-limit buckets")
}
