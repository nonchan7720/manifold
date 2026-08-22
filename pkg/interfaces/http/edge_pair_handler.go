package httphandler

import (
	"encoding/json"
	"net/http"

	"github.com/n-creativesystem/go-packages/lib/trace"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
)

// EdgePairHandler serves POST /edge/pair, exchanging a pairing code (issued
// via the create_pairing_code tool) for a long-lived edge token.
type EdgePairHandler struct {
	pairing *edgeservices.PairingService
}

// NewEdgePairHandler creates an EdgePairHandler backed by pairing.
func NewEdgePairHandler(pairing *edgeservices.PairingService) *EdgePairHandler {
	return &EdgePairHandler{pairing: pairing}
}

// Pair handles POST /edge/pair {"code": "12345678", "token": "<existing edge
// token, optional>"} -> {"token": "..."}.
func (h *EdgePairHandler) Pair(w http.ResponseWriter, r *http.Request) {
	ctx := trace.StartSpan(r.Context(), "httphandler/EdgePairHandler/Pair")
	var err error
	defer func() { trace.EndSpan(ctx, err) }()

	writeError := func(status int, code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		Code  string `json:"code"`
		Token string `json:"token"`
	}
	if err = json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(http.StatusBadRequest, "invalid_request")
		return
	}
	if req.Code == "" {
		writeError(http.StatusBadRequest, "invalid_request")
		return
	}

	token, err := h.pairing.ExchangeCode(ctx, req.Code, req.Token)
	if err != nil {
		writeError(http.StatusBadRequest, "invalid_code")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
}
