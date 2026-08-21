package httphandler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
	"github.com/stretchr/testify/require"
)

func newTestEdgePairHandler(t *testing.T) (*EdgePairHandler, *edgeservices.PairingService) {
	t.Helper()
	storeClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeClient.Close() })
	pairing := edgeservices.NewPairingService(storeClient)
	return NewEdgePairHandler(pairing), pairing
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
