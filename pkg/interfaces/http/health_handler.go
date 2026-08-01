package httphandler

import (
	"encoding/json"
	"net/http"
)

// HealthHandler は liveness/readiness probe 向けのヘルスチェックエンドポイントを提供する。
type HealthHandler struct{}

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
