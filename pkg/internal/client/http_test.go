package client

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsPrivateIP(t *testing.T) {
	privates := []string{"127.0.0.1", "10.1.2.3", "192.168.0.1", "172.16.0.1", "169.254.1.1", "::1"}
	for _, s := range privates {
		ip := net.ParseIP(s)
		require.NotNil(t, ip)
		require.True(t, isPrivateIP(ip), "expected private: %s", s)
	}

	publics := []string{"8.8.8.8", "1.1.1.1", "203.0.113.1"}
	for _, s := range publics {
		ip := net.ParseIP(s)
		require.NotNil(t, ip)
		require.False(t, isPrivateIP(ip), "expected public: %s", s)
	}
}
