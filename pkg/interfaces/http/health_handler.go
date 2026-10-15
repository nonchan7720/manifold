package httphandler

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// HealthHandler は liveness/readiness probe 向けのヘルスチェックエンドポイントを提供する。
type HealthHandler struct {
	notReady atomic.Bool
}

// NewHealthHandler は HealthHandler を生成する。
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// Healthz はプロセスが応答可能であることを示すエンドポイント。常に200 okを返す。
func (h *HealthHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// SetNotReady は readiness を落とす。以降 Readyz は 503 を返す。
// シャットダウン開始時に呼び、ロードバランサーや Kubernetes のエンドポイントから外させる。
func (h *HealthHandler) SetNotReady() {
	h.notReady.Store(true)
}

// Readyz はトラフィックを受け付けられることを示すエンドポイント。
// SetNotReady 後（シャットダウン開始後）は 503 を返す。
func (h *HealthHandler) Readyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.notReady.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "shutting down"})
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
