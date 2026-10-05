package config

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"maps"
	"regexp"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/nonchan7720/manifold/pkg/internal/telemetry"
)

type Config struct {
	Gateway   Gateway `mapstructure:"gateway"`
	MCPServer Servers `mapstructure:"mcpServers"`
	// Agents は検証後に MCPServer へ（transport a2a として）マージされる。
	// mergeAgentsIntoServers を参照。
	Agents Agents `mapstructure:"agents"`

	Redis  *RedisConfig  `mapstructure:"redis"`
	SQLite *SQLiteConfig `mapstructure:"sqlite"`
	Memory *MemoryConfig `mapstructure:"memory"`

	Telemetry telemetry.Config `mapstructure:"telemetry"`

	FileFetch FileFetchConfig `mapstructure:"fileFetch"`

	Storage Storage `mapstructure:"storage"`

	Identities map[string]*IdentityProfile `mapstructure:"identities"`

	Authz AuthzConfig `mapstructure:"authz"`

	OAuth OAuthConfig `mapstructure:"oauth"`

	Audit AuditConfig `mapstructure:"audit"`
}

// URL パスセグメントとして使われるサーバー名として妥当な文字集合。
// ドットは除外しつつ、既存設定で使われてきたハイフン区切りの名前を壊さないようハイフンを許可する。
var pathRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// edgeContextKey carries the effective Gateway.Edge config into Server
// validation, so a reverse Server's identity requirement can depend on
// whether pairing.type is static without Server needing a Gateway reference.
type edgeContextKey struct{}

func (c *Config) ValidateWithContext(ctx context.Context) error {
	ctx = context.WithValue(ctx, edgeContextKey{}, c.Gateway.Edge.WithDefaults())
	ctx = context.WithValue(ctx, identitiesContextKey{}, c.Identities)
	return validation.ValidateStructWithContext(
		ctx,
		c,
		// An empty encryptKey is only acceptable with the in-memory store,
		// where ApplyEphemeralDefaults generates one at gateway startup.
		validation.Field(&c.Gateway, validation.By(func(any) error {
			if c.Gateway.EncryptKey == "" && !c.UsesEphemeralStore() {
				return validation.Errors{"encryptKey": validation.ErrRequired}
			}
			return nil
		})),
		validation.Field(&c.Identities),
		validation.Field(&c.MCPServer, validation.By(func(value any) error {
			mp, ok := value.(Servers)
			if !ok {
				return fmt.Errorf("type error: %T", value)
			}
			origins := map[string]string{}
			for key, srv := range mp {
				if !pathRegex.MatchString(key) {
					return fmt.Errorf("key '%s' contains invalid characters", key)
				}
				if srv.Transport != MCPTransportReverse {
					continue
				}
				normalized, err := NormalizeOrigin(srv.Origin)
				if err != nil {
					continue // Server.ValidateWithContext がこの不正値自体を報告する
				}
				if other, dup := origins[normalized]; dup {
					return fmt.Errorf(
						"origin %q is used by both server '%s' and '%s'; origins must be unique",
						normalized, other, key,
					)
				}
				origins[normalized] = key
			}
			return nil
		})),
		validation.Field(&c.Agents, validation.By(func(value any) error {
			mp, ok := value.(Agents)
			if !ok {
				return fmt.Errorf("type error: %T", value)
			}
			for key := range mp {
				if !pathRegex.MatchString(key) {
					return fmt.Errorf("key '%s' contains invalid characters", key)
				}
				if _, dup := c.MCPServer[key]; dup {
					return fmt.Errorf(
						"name '%s' is used by both agents and mcpServers; names must be unique",
						key,
					)
				}
			}
			return nil
		})),
		// サービスは mcpServers と agents をまたいでまとめられるため、両方を
		// 合わせた一覧で表示名の食い違いを検証する。
		validation.Field(&c.Agents, validation.By(func(any) error {
			servers := maps.Clone(c.MCPServer)
			if servers == nil {
				servers = Servers{}
			}
			for name, agent := range c.Agents {
				if agent != nil {
					srv := agent.Server()
					srv.Name = name
					servers[name] = srv
				}
			}
			return validateServiceNames(servers)
		})),
		// redis / sqlite / memory のいずれも未設定ならインメモリストアで動く
		// （UsesEphemeralStore 参照）ため、どれも必須にしない。
		validation.Field(&c.Redis),
		validation.Field(&c.SQLite),
		validation.Field(&c.Storage),
		validation.Field(&c.Audit),
		validation.Field(&c.Authz),
		validation.Field(&c.OAuth),
	)
}

type Gateway struct {
	Port int `mapstructure:"port"`

	Key  string `mapstructure:"key"`
	Cert string `mapstructure:"cert"`

	EncryptKey string `mapstructure:"encryptKey"`

	Edge EdgeConfig `mapstructure:"edge"`

	SpecRefresh SpecRefreshConfig `mapstructure:"specRefresh"`

	// ToolSearch は、見えるツールが多いエンドポイントの tools/list を合成ツール
	// tool_search に置き換える閾値などの設定。
	ToolSearch ToolSearchConfig `mapstructure:"toolSearch"`
}

func (c Gateway) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(
		ctx,
		&c,
		validation.Field(&c.EncryptKey,
			validation.When(c.EncryptKey != "",
				validation.By(func(value any) error {
					v, ok := value.(string)
					if !ok {
						return fmt.Errorf("must be string type")
					}
					decoded, err := base64.StdEncoding.DecodeString(v)
					// AES-256 requires 32 bytes key
					if len(decoded) != 32 {
						return fmt.Errorf("key must be 32 bytes for AES-256")
					}
					return err
				}),
			),
		),
		validation.Field(&c.Edge),
		validation.Field(&c.SpecRefresh),
		validation.Field(&c.ToolSearch),
	)
}

// UsesEphemeralStore reports whether the gateway keeps sessions and tokens in
// process memory (see cmd.newStoreClient): sqlite.path unset and either
// memory.enabled or no redis configured.
func (c *Config) UsesEphemeralStore() bool {
	if c.SQLite != nil && c.SQLite.Path != "" {
		return false
	}
	if c.Memory != nil && c.Memory.Enabled {
		return true
	}
	return c.Redis == nil
}

// ApplyEphemeralDefaults fills in gateway.encryptKey with a random key when it
// is unset and the store is in memory: the key only protects tokens held by
// this process, which are lost on restart anyway. A persistent store (redis,
// sqlite) still requires a configured key, so tokens stay readable across
// restarts and replicas. Only the gateway server needs the key, so it calls
// this at startup rather than the config loader.
func (c *Config) ApplyEphemeralDefaults() (generated bool, err error) {
	if c.Gateway.EncryptKey != "" || !c.UsesEphemeralStore() {
		return false, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return false, fmt.Errorf("generate gateway.encryptKey: %w", err)
	}
	c.Gateway.EncryptKey = base64.StdEncoding.EncodeToString(key)
	return true, nil
}
