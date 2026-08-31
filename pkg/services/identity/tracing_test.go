package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
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

// --- NewResolver / NewResolvers ---

func TestNewResolver_Header_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)

	_, err := NewResolver(t.Context(), "sharedKeyUser", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-User-Id",
	}, nil)
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "identity/NewResolver"))
}

func TestNewResolvers_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)

	_, err := NewResolvers(t.Context(), map[string]*config.IdentityProfile{
		"sharedKeyUser": {Source: config.IdentitySourceHeader, Header: "X-User-Id"},
	}, nil)
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "identity/NewResolvers"))
	require.True(t, hasSpanNamed(exporter, "identity/NewResolver"))
}

func TestHeaderResolver_Resolve_RecordsSpan(t *testing.T) {
	r, err := NewResolver(t.Context(), "sharedKeyUser", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-User-Id",
	}, nil)
	require.NoError(t, err)

	exporter := setupTracerProvider(t)
	_, err = r.Resolve(t.Context(), newHeaderRequest(t, "X-User-Id", "user-a"))
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "identity/headerResolver/Resolve"))
}

func TestJWTResolver_NewResolver_RecordsSpan(t *testing.T) {
	f := newJWTTestFixture(t)
	exporter := setupTracerProvider(t)

	_, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "identity/newJWTResolver"))
}

func TestJWTResolver_Resolve_RecordsSpan(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	exporter := setupTracerProvider(t)
	token := f.sign(t, newJWTClaims("user-a", f.issuer))
	_, err = r.Resolve(t.Context(), newBearerRequest(token))
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "identity/jwtResolver/Resolve"))
}

func TestIntrospectionResolver_Resolve_RecordsSpan(t *testing.T) {
	s := newIntrospectionTestServer(t)
	s.setResponse("cred-a", true, "user-a")
	r := newTestIntrospectionResolver(t, s, time.Minute)

	exporter := setupTracerProvider(t)
	_, err := r.Resolve(t.Context(), newIntrospectionRequest("cred-a"))
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "identity/introspectionResolver/Resolve"))
	require.True(t, hasSpanNamed(exporter, "identity/introspectionResolver/refresh"))
	require.True(t, hasSpanNamed(exporter, "identity/introspectionResolver/introspect"))
}
