// Command stub is the fake Petstore API behind the Postman CLI end-to-end
// run (tests/postman/run.sh). It serves the petstore OpenAPI fixture at
// /openapi.json and answers the handful of operations the collection calls,
// echoing the Authorization header it received so the collection can check
// that the gateway forwards the caller's token.
package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen address")
	spec := flag.String("spec", "", "path of the OpenAPI document served at /openapi.json")
	flag.Parse()
	if *spec == "" {
		slog.Error("-spec is required")
		os.Exit(2)
	}
	specBytes, err := os.ReadFile(*spec)
	if err != nil {
		slog.Error("read spec", slog.Any("error", err))
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(specBytes)
	})
	mux.HandleFunc("GET /api/v3/pet/findByStatus", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]any{{
			"id": 1, "name": "doggie", "status": r.URL.Query().Get("status"),
		}})
	})
	mux.HandleFunc("GET /api/v3/pet/findByTags", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{})
	})
	mux.HandleFunc("GET /api/v3/pet/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid pet id", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"id":   id,
			"name": "doggie",
			// 呼び出し元のトークンがゲートウェイ経由で届いたことをコレクション側で確認する。
			"auth": r.Header.Get("Authorization"),
		})
	})
	mux.HandleFunc("DELETE /api/v3/pet/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"deleted": r.PathValue("id")})
	})
	mux.HandleFunc("GET /api/v3/store/inventory", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"available": 3, "sold": 1})
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	slog.Info("stub listening", slog.String("addr", *addr))
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("serve", slog.Any("error", err))
		os.Exit(1)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", slog.Any("error", err))
	}
}
