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

// --- reverse サーバーと edge 設定の相互作用 ---

func TestConfig_ValidateWithContext_Reverse_StaticPairing_IdentityNotRequired(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"app1": {
			Description: "app1",
			Transport:   MCPTransportReverse,
			Origin:      "https://app1.example.com",
		},
	})
	cfg.Gateway.Edge = EdgeConfig{
		Auth:    EdgeAuthPairing,
		Pairing: PairingConfig{Type: PairingTypeStatic},
	}
	err := cfg.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestConfig_ValidateWithContext_Reverse_RemotePairing_RequiresIdentity(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"app1": {
			Description: "app1",
			Transport:   MCPTransportReverse,
			Origin:      "https://app1.example.com",
		},
	})
	cfg.Gateway.Edge = EdgeConfig{
		Auth:    EdgeAuthPairing,
		Pairing: PairingConfig{Type: PairingTypeRemote},
	}
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestConfig_ValidateWithContext_Reverse_DuplicateOrigin_Invalid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"app1": {
			Description: "app1",
			Transport:   MCPTransportReverse,
			Origin:      "https://app.example.com",
			Identity:    "oauth",
		},
		"app2": {
			Description: "app2",
			Transport:   MCPTransportReverse,
			Origin:      "https://APP.example.com", // 正規化後に app1 と衝突する
			Identity:    "oauth",
		},
	})
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestConfig_ValidateWithContext_Reverse_DistinctOrigins_Valid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"app1": {
			Description: "app1",
			Transport:   MCPTransportReverse,
			Origin:      "https://app1.example.com",
			Identity:    "oauth",
		},
		"app2": {
			Description: "app2",
			Transport:   MCPTransportReverse,
			Origin:      "https://app2.example.com",
			Identity:    "oauth",
		},
	})
	err := cfg.ValidateWithContext(t.Context())
	require.NoError(t, err)
}
