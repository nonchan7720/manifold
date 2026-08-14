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

// --- ストレージバックエンド（Redis / SQLite / Memory）の相互排他バリデーション ---

func TestConfig_ValidateWithContext_Memory_Only_Valid(t *testing.T) {
	cfg := &Config{
		Gateway: Gateway{EncryptKey: validEncryptKey},
		Memory:  &MemoryConfig{Enabled: true},
	}
	err := cfg.ValidateWithContext(t.Context())
	require.NoError(t, err, "memoryのみの設定でも有効になるべき")
}

func TestConfig_ValidateWithContext_NoStorageBackend_Invalid(t *testing.T) {
	cfg := &Config{
		Gateway: Gateway{EncryptKey: validEncryptKey},
	}
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err, "Redis/SQLite/Memoryのいずれも未設定ならエラーになるべき")
}

func TestConfig_ValidateWithContext_SQLiteOnly_StillValid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{})
	err := cfg.ValidateWithContext(t.Context())
	require.NoError(t, err, "既存のSQLiteのみの構成は引き続き有効であるべき")
}

// --- gateway.baseURL のバリデーション ---

func TestGateway_ValidateWithContext_BaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{"empty (optional)", "", false},
		{"valid https", "https://gateway.example.com", false},
		{"valid http", "http://gateway.example.com", false},
		{"trailing slash", "https://gateway.example.com/", false},
		{"with path", "https://gateway.example.com/prefix", true},
		{"with query", "https://gateway.example.com?x=1", true},
		{"bare query delimiter", "https://gateway.example.com?", true},
		{"with fragment", "https://gateway.example.com#frag", true},
		{"no scheme", "gateway.example.com", true},
		{"non-http scheme", "ftp://gateway.example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := Gateway{EncryptKey: validEncryptKey, BaseURL: tt.baseURL}
			err := g.ValidateWithContext(t.Context())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
