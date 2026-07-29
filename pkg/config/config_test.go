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
			// ToolSearchConfig{}.WithDefaults() を経由し、DigestMaxTools が -1
			// （ゼロ値のまま渡すと ValidateWithContext が常にエラーにする）になるようにする。
			ToolSearch: ToolSearchConfig{}.WithDefaults(),
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

// --- gateway.toolSearch のバリデーションが Config 経由でも効くこと ---

func TestConfig_ValidateWithContext_ToolSearch_NegativeThreshold_Invalid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{})
	cfg.Gateway.ToolSearch = ToolSearchConfig{Threshold: -1}
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestConfig_ValidateWithContext_ToolSearch_Defaults_Valid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{})
	cfg.Gateway.ToolSearch = ToolSearchConfig{}.WithDefaults()
	err := cfg.ValidateWithContext(t.Context())
	require.NoError(t, err)
}
