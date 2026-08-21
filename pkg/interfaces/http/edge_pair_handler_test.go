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
