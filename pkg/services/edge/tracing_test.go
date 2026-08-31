package edge

import (
	"strings"
	"testing"

	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
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

func TestPairingService_IssueCode_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	s := newTestPairingService(t)

	_, err := s.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "edge/PairingService/IssueCode"))
}

func TestPairingService_ExchangeCode_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	s := newTestPairingService(t)
	code := seedPairingCode(t, s, domainedge.StaticIdentityKey)

	_, err := s.ExchangeCode(t.Context(), code, "")
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "edge/PairingService/ExchangeCode"))
}

func TestPairingService_Authenticate_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	s := newTestPairingService(t)
	code := seedPairingCode(t, s, domainedge.StaticIdentityKey)
	token, err := s.ExchangeCode(t.Context(), code, "")
	require.NoError(t, err)
	exporter.Reset()

	_, err = s.Authenticate(t.Context(), token)
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "edge/PairingService/Authenticate"))
}

func TestPairingService_Revoke_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	s := newTestPairingService(t)
	code := seedPairingCode(t, s, domainedge.StaticIdentityKey)
	token, err := s.ExchangeCode(t.Context(), code, "")
	require.NoError(t, err)
	exporter.Reset()

	err = s.Revoke(t.Context(), token)
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "edge/PairingService/Revoke"))
}

func TestPairingService_RateLimitPairAttempt_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	s := newTestPairingService(t)

	err := s.RateLimitPairAttempt(t.Context(), "203.0.113.1")
	require.NoError(t, err)

	require.True(t, hasSpanNamed(exporter, "edge/PairingService/RateLimitPairAttempt"))
}

func TestInMemoryRegistry_Bind_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	r := NewInMemoryRegistry()

	r.Bind(t.Context(), domainedge.Binding{
		IdentityKey: domainedge.StaticIdentityKey,
		Origin:      "https://app1.example.com",
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}, "handle-1")

	require.True(t, hasSpanNamed(exporter, "edge/InMemoryRegistry/Bind"))
}

func TestInMemoryRegistry_Resolve_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	r := NewInMemoryRegistry()

	r.Resolve(t.Context(), domainedge.StaticIdentityKey, "https://app1.example.com")

	require.True(t, hasSpanNamed(exporter, "edge/InMemoryRegistry/Resolve"))
}

func TestInMemoryRegistry_Unbind_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	r := NewInMemoryRegistry()

	r.Unbind(t.Context(), domainedge.StaticIdentityKey, "https://app1.example.com", "session-1")

	require.True(t, hasSpanNamed(exporter, "edge/InMemoryRegistry/Unbind"))
}

func TestInMemoryRegistry_DropConnection_RecordsSpan(t *testing.T) {
	exporter := setupTracerProvider(t)
	r := NewInMemoryRegistry()

	r.DropConnection(t.Context(), "conn-1")

	require.True(t, hasSpanNamed(exporter, "edge/InMemoryRegistry/DropConnection"))
}
