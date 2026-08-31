package authz

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
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

func hasSpanNamed(exporter *tracetest.InMemoryExporter, name string) bool {
	for _, s := range exporter.GetSpans() {
		if strings.HasSuffix(s.Name, name) {
			return true
		}
	}
	return false
}

// --- span coverage for OPADecider's public methods ---

func TestOPADecider_Allow_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	s := newOPAStub(t)
	s.response = `{"result": true}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.Allow(
		t.Context(), testPrincipal(), ToolRef{Server: "billing-svc", Name: "create_invoice"},
	)
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "authz/OPADecider/Allow"))
	require.True(t, hasSpanNamed(exporter, "authz/OPADecider/post"))
}

func TestOPADecider_AllowedTools_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	s := newOPAStub(t)
	s.response = `{"result": []}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.AllowedTools(
		t.Context(), testPrincipal(), []ToolRef{{Server: "billing-svc", Name: "create_invoice"}},
	)
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "authz/OPADecider/AllowedTools"))
}

func TestOPADecider_AllowCatalog_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	s := newOPAStub(t)
	s.response = `{"result": true}`
	d := NewOPADecider(testAuthzConfig(s.srv.URL), nil)

	_, err := d.AllowCatalog(t.Context(), testPrincipal())
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "authz/OPADecider/AllowCatalog"))
}
