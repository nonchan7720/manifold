package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func setupTracerProvider(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	original := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(original)
		exporter.Reset()
	})
	return exporter
}

func TestRecover_NoPanic(t *testing.T) {
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := Recover(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	require.True(t, called)
	require.Equal(t, http.StatusOK, rw.Code)
}

func TestRecover_WithPanic(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	})
	handler := Recover(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	require.Equal(t, http.StatusInternalServerError, rw.Code)
	require.Equal(t, "application/json", rw.Header().Get("Content-Type"))

	var body map[string]string
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, "Internal server error", body["error"])
}

func TestRecover_WithPanic_SpanRecordsError(t *testing.T) {
	exporter := setupTracerProvider(t)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	})
	handler := Recover(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status.Code)
	require.Contains(t, spans[0].Status.Description, "panic")
}

func TestRecover_NoPanic_SpanNoError(t *testing.T) {
	exporter := setupTracerProvider(t)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := Recover(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Unset, spans[0].Status.Code)
}
