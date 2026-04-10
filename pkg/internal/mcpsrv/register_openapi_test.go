package mcpsrv

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterOpenAPI(t *testing.T) {
	r, err := RegisterOpenAPI("./swagger2.json", "")
	require.NoError(t, err)
	require.NotNil(t, r)
}
