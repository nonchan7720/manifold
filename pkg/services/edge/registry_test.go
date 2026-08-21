package edge

import (
	"testing"

	"github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/stretchr/testify/require"
)

func TestInMemoryRegistry_Resolve_NotBound(t *testing.T) {
	r := NewInMemoryRegistry()
	_, ok := r.Resolve(t.Context(), edge.StaticIdentityKey, "https://app1.example.com")
	require.False(t, ok)
}

func TestInMemoryRegistry_BindThenResolve(t *testing.T) {
	r := NewInMemoryRegistry()
	binding := edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      "https://app1.example.com",
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}
	_, hadPrevious := r.Bind(t.Context(), binding, "handle-1")
	require.False(t, hadPrevious)

	handle, ok := r.Resolve(t.Context(), edge.StaticIdentityKey, "https://app1.example.com")
	require.True(t, ok)
	require.Equal(t, "handle-1", handle)
}

func TestInMemoryRegistry_Bind_LastWriterWins(t *testing.T) {
	r := NewInMemoryRegistry()
	origin := "https://app1.example.com"
	first := edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      origin,
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}
	r.Bind(t.Context(), first, "handle-1")

	second := edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      origin,
		AppSession:  "session-2",
		ConnID:      "conn-1",
	}
	previous, hadPrevious := r.Bind(t.Context(), second, "handle-2")
	require.True(t, hadPrevious)
	require.Equal(t, "handle-1", previous)

	handle, ok := r.Resolve(t.Context(), edge.StaticIdentityKey, origin)
	require.True(t, ok)
	require.Equal(t, "handle-2", handle)
}

func TestInMemoryRegistry_Bind_DoesNotAffectOtherOrigins(t *testing.T) {
	r := NewInMemoryRegistry()
	r.Bind(t.Context(), edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      "https://app1.example.com",
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}, "handle-app1")
	r.Bind(t.Context(), edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      "https://app2.example.com",
		AppSession:  "session-2",
		ConnID:      "conn-1",
	}, "handle-app2")

	handle, ok := r.Resolve(t.Context(), edge.StaticIdentityKey, "https://app1.example.com")
	require.True(t, ok)
	require.Equal(t, "handle-app1", handle)
}

func TestInMemoryRegistry_Unbind_MatchingAppSession(t *testing.T) {
	r := NewInMemoryRegistry()
	origin := "https://app1.example.com"
	r.Bind(t.Context(), edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      origin,
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}, "handle-1")

	handle, ok := r.Unbind(t.Context(), edge.StaticIdentityKey, origin, "session-1")
	require.True(t, ok)
	require.Equal(t, "handle-1", handle)

	_, ok = r.Resolve(t.Context(), edge.StaticIdentityKey, origin)
	require.False(t, ok)
}

func TestInMemoryRegistry_Unbind_StaleAppSession_NoOp(t *testing.T) {
	// 世代交代後に旧世代からの app.down が届いても、現行世代を down させてはならない。
	r := NewInMemoryRegistry()
	origin := "https://app1.example.com"
	r.Bind(t.Context(), edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      origin,
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}, "handle-1")
	r.Bind(t.Context(), edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      origin,
		AppSession:  "session-2",
		ConnID:      "conn-1",
	}, "handle-2")

	_, ok := r.Unbind(t.Context(), edge.StaticIdentityKey, origin, "session-1")
	require.False(t, ok)

	handle, ok := r.Resolve(t.Context(), edge.StaticIdentityKey, origin)
	require.True(t, ok)
	require.Equal(t, "handle-2", handle)
}

func TestInMemoryRegistry_DropConnection_RemovesAllOwnedBindings(t *testing.T) {
	r := NewInMemoryRegistry()
	r.Bind(t.Context(), edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      "https://app1.example.com",
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}, "handle-app1")
	r.Bind(t.Context(), edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      "https://app2.example.com",
		AppSession:  "session-2",
		ConnID:      "conn-1",
	}, "handle-app2")
	r.Bind(t.Context(), edge.Binding{
		IdentityKey: edge.StaticIdentityKey,
		Origin:      "https://app3.example.com",
		AppSession:  "session-3",
		ConnID:      "conn-other",
	}, "handle-app3")

	dropped := r.DropConnection(t.Context(), "conn-1")
	require.Len(t, dropped, 2)

	_, ok := r.Resolve(t.Context(), edge.StaticIdentityKey, "https://app1.example.com")
	require.False(t, ok)
	_, ok = r.Resolve(t.Context(), edge.StaticIdentityKey, "https://app2.example.com")
	require.False(t, ok)

	// 別接続のバインディングは残る
	handle, ok := r.Resolve(t.Context(), edge.StaticIdentityKey, "https://app3.example.com")
	require.True(t, ok)
	require.Equal(t, "handle-app3", handle)
}

func TestInMemoryRegistry_ImplementsDomainRegistry(t *testing.T) {
	var _ edge.Registry = NewInMemoryRegistry()
}
