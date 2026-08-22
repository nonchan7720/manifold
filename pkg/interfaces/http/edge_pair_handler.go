package httphandler

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/netinternet/remoteaddr"
	"github.com/nonchan7720/manifold/pkg/config"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
)

var (
	xForwardedForHeaders  = []string{"X-Forwarded-For"}
	cfConnectingIPHeaders = []string{"CF-Connecting-IP"}
)

// EdgePairHandler serves POST /edge/pair, exchanging a pairing code (issued
// via the create_pairing_code tool) for a long-lived edge token.
type EdgePairHandler struct {
	pairing         *edgeservices.PairingService
	forwarderGroups []*remoteaddr.Addr
}

// NewEdgePairHandler creates an EdgePairHandler backed by pairing. Each
// trusted-proxy group gets its own remoteaddr.Addr scoped to the single
// header that group's proxy actually sets: RFC1918 (always, since it can't
// be spoofed over the internet) and edge.trustedForwarders (an
// operator-supplied CIDR, e.g. an ALB/Ingress subnet) both read
// X-Forwarded-For, while Cloudflare's published ranges — opt-in via
// edgeCfg.TrustCloudflare — read CF-Connecting-IP. Keeping the headers
// scoped per group stops a request that only arrives through one trusted
// proxy from forging the header another group would trust — see
// docs/design/webmcp-reverse-gateway-phase2.ja.md「Phase 1 からの持ち越し判断事項」.
func NewEdgePairHandler(
	pairing *edgeservices.PairingService,
	edgeCfg config.EdgeConfig,
) *EdgePairHandler {
	groups := []*remoteaddr.Addr{
		remoteaddr.Parse().SetForwarders(rfc1918Forwarders).SetHeaders(xForwardedForHeaders),
	}
	if edgeCfg.TrustCloudflare {
		groups = append(groups, remoteaddr.Parse().
			SetForwarders(cloudflareForwarders).
			SetHeaders(cfConnectingIPHeaders))
	}
	if len(edgeCfg.TrustedForwarders) > 0 {
		groups = append(groups, remoteaddr.Parse().
			SetForwarders(edgeCfg.TrustedForwarders).
			SetHeaders(xForwardedForHeaders))
	}
	return &EdgePairHandler{pairing: pairing, forwarderGroups: groups}
}

// resolveIP returns r's caller IP for /edge/pair rate limiting: the raw TCP
// peer address, unless it falls within a trusted group's forwarders, in
// which case that group's own header is trusted instead (see
// NewEdgePairHandler).
func (h *EdgePairHandler) resolveIP(r *http.Request) string {
	raw, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	for _, group := range h.forwarderGroups {
		if ip, _ := group.IP(r); ip != "" && ip != raw {
			return ip
		}
	}
	return raw
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

	ip := h.resolveIP(r)
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
		if errors.Is(err, edgeservices.ErrInvalidCode) {
			writeError(http.StatusBadRequest, "invalid_code")
		} else {
			writeError(http.StatusInternalServerError, "internal_error")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
}
