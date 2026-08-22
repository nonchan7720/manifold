package edge

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdentityKeyFromContext_Unset_ReturnsFalse(t *testing.T) {
	_, ok := IdentityKeyFromContext(context.Background())
	require.False(t, ok)
}

func TestIdentityKeyFromContext_Set_ReturnsKey(t *testing.T) {
	ctx := WithIdentityKey(context.Background(), IdentityKey("oauth:user-a"))
	key, ok := IdentityKeyFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, IdentityKey("oauth:user-a"), key)
}

func TestWithIdentityKey_DoesNotMutateParentContext(t *testing.T) {
	parent := context.Background()
	_ = WithIdentityKey(parent, IdentityKey("oauth:user-a"))
	_, ok := IdentityKeyFromContext(parent)
	require.False(t, ok)
}
