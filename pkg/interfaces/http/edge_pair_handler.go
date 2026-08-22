package httphandler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/netinternet/remoteaddr"
	"github.com/nonchan7720/manifold/pkg/config"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
)

// EdgePairHandler serves POST /edge/pair, exchanging a pairing code (issued
// via the create_pairing_code tool) for a long-lived edge token.
type EdgePairHandler struct {
	pairing    *edgeservices.PairingService
	remoteAddr *remoteaddr.Addr
}

// NewEdgePairHandler creates an EdgePairHandler backed by pairing. The
// forwarders trusted to resolve RateLimitPairAttempt's caller IP are RFC1918
// (always, since it can't be spoofed over the internet) plus, opt-in via
// edgeCfg, Cloudflare's published ranges and any operator-supplied CIDRs —
// see docs/design/webmcp-reverse-gateway-phase2.ja.md「Phase 1 からの持ち越し判断事項」.
func NewEdgePairHandler(
	pairing *edgeservices.PairingService,
	edgeCfg config.EdgeConfig,
) *EdgePairHandler {
	remoteAddr := remoteaddr.Parse().SetForwarders(rfc1918Forwarders)
	if edgeCfg.TrustCloudflare {
		remoteAddr.AddForwarders(cloudflareForwarders)
	}
	if len(edgeCfg.TrustedForwarders) > 0 {
		remoteAddr.AddForwarders(edgeCfg.TrustedForwarders)
	}
	return &EdgePairHandler{pairing: pairing, remoteAddr: remoteAddr}
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

	ip, _ := h.remoteAddr.IP(r)
	if err = h.pairing.RateLimitPairAttempt(ctx, ip); err != nil {
		if errors.Is(err, edgeservices.ErrIPRateLimited) {
			writeError(http.StatusTooManyRequests, "rate_limited")
		} else {
			writeError(http.StatusInternalServerError, "internal_error")
		}
		return
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
