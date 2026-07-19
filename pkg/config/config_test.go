package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// validEncryptKey は 32 バイトを base64 エンコードしたダミーの AES-256 鍵（テスト専用）。
const validEncryptKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func newValidConfigWithServers(servers Servers) *Config {
	return &Config{
		Gateway: Gateway{
			EncryptKey: validEncryptKey,
		},
		MCPServer: servers,
		SQLite:    &SQLiteConfig{Path: ":memory:"},
	}
}

// --- mcpServers キーのバリデーション（ハイフン許可） ---

func TestConfig_ValidateWithContext_ServerKey_Hyphen_Valid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"my-server": {
			Description: "test",
			Transport:   MCPTransportHTTP,
			URL:         "http://example.com",
		},
	})
	err := cfg.ValidateWithContext(t.Context())
	require.NoError(t, err, "hyphenated server keys should remain valid")
}

func TestConfig_ValidateWithContext_ServerKey_Underscore_Valid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"my_server": {
			Description: "test",
			Transport:   MCPTransportHTTP,
			URL:         "http://example.com",
		},
	})
	err := cfg.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestConfig_ValidateWithContext_ServerKey_Alphanumeric_Valid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"server123": {
			Description: "test",
			Transport:   MCPTransportHTTP,
			URL:         "http://example.com",
		},
	})
	err := cfg.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestConfig_ValidateWithContext_ServerKey_Dot_Invalid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"my.server": {
			Description: "test",
			Transport:   MCPTransportHTTP,
			URL:         "http://example.com",
		},
	})
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err, "dots must remain excluded from server keys")
}

func TestConfig_ValidateWithContext_ServerKey_Space_Invalid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"my server": {
			Description: "test",
			Transport:   MCPTransportHTTP,
			URL:         "http://example.com",
		},
	})
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err)
}
