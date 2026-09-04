package config

import (
	"context"
	"fmt"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

type MCPTransport string

const (
	MCPTransportHTTP    MCPTransport = "http"
	MCPTransportStdio   MCPTransport = "stdio"
	MCPTransportReverse MCPTransport = "reverse"
)

// DefaultCallTimeout is used when Server.CallTimeout is unset (<= 0) for a
// reverse transport server.
const DefaultCallTimeout = 60 * time.Second

type Servers map[string]*Server

type Server struct {
	Name         string
	Description  string            `mapstructure:"description"`
	BaseURL      string            `mapstructure:"baseURL"`
	Spec         string            `mapstructure:"spec"` // ファイル or http(s)（OpenAPI モード）
	ExtraHeaders map[string]string `mapstructure:"headers"`

	// nil は gateway.specRefresh.interval を使う、0 はこのサーバーのみリフレッシュ無効。
	SpecRefreshInterval *time.Duration `mapstructure:"specRefreshInterval"`

	// Tools は静的ツールカタログ（生成物）関連の設定。File が指定されると、
	// 起動・リフレッシュで spec を取得せず生成物ファイルからツールを読み込む。
	Tools *ToolsConfig `mapstructure:"tools"`

	AuthValue     *AuthValue     `mapstructure:"authValue"`
	OAuth2        *OAuth2        `mapstructure:"oauth2"`
	TokenExchange *TokenExchange `mapstructure:"tokenExchange"`

	// MCP バックエンドモード用（Spec が空のとき有効）
	Transport MCPTransport      `mapstructure:"transport"`
	URL       string            `mapstructure:"url"`     // streamable_http 用
	Command   string            `mapstructure:"command"` // stdio 用
	Args      []string          `mapstructure:"args"`
	Env       map[string]string `mapstructure:"env"`

	// reverse トランスポート用（WebMCP reverse connection gateway）
	Origin      string        `mapstructure:"origin"`      // ブリッジ対象タブの許可 origin
	Identity    string        `mapstructure:"identity"`    // identities プロファイル名の参照
	CallTimeout time.Duration `mapstructure:"callTimeout"` // 未設定/0以下は DefaultCallTimeout
}

// CallTimeoutOrDefault returns CallTimeout, falling back to DefaultCallTimeout
// when unset.
func (s Server) CallTimeoutOrDefault() time.Duration {
	if s.CallTimeout <= 0 {
		return DefaultCallTimeout
	}
	return s.CallTimeout
}

// GeneratedToolsFile returns tools.file, or "" when unset (or Tools itself
// is unset).
func (s Server) GeneratedToolsFile() string {
	if s.Tools == nil {
		return ""
	}
	return s.Tools.File
}

// EffectiveSpecRefreshInterval returns the refresh interval for this server,
// falling back to the gateway-wide default. Only OpenAPI mode servers refresh;
// others always return 0. A server with tools.file set never refreshes
// (it starts from the generated file, not the live spec), regardless of the
// gateway-wide default.
func (s Server) EffectiveSpecRefreshInterval(global time.Duration) time.Duration {
	if s.Spec == "" {
		return 0
	}
	if s.GeneratedToolsFile() != "" {
		return 0
	}
	if s.SpecRefreshInterval != nil {
		return *s.SpecRefreshInterval
	}
	return global
}

func (s Server) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(
		ctx,
		&s,
		validation.Field(&s.Description, validation.Required),
		validation.Field(&s.BaseURL, validation.When(s.Spec != "", validation.Required)),
		validation.Field(
			&s.Transport,
			validation.When(
				s.Spec == "",
				validation.In(MCPTransportHTTP, MCPTransportStdio, MCPTransportReverse),
			),
			validation.By(func(value any) error {
				if s.Transport != MCPTransportReverse {
					return nil
				}
				switch {
				case s.Spec != "":
					return fmt.Errorf("reverse transport does not support spec (OpenAPI mode)")
				case s.AuthValue != nil, s.OAuth2 != nil, s.TokenExchange != nil:
					return fmt.Errorf(
						"reverse transport does not support authValue/oauth2/tokenExchange " +
							"(the page's own session authenticates its tools)",
					)
				case s.Command != "":
					return fmt.Errorf("reverse transport does not support command")
				case s.URL != "":
					return fmt.Errorf("reverse transport does not support url")
				default:
					return nil
				}
			}),
		),
		validation.Field(
			&s.URL,
			validation.When(s.Spec == "" && s.Transport == MCPTransportHTTP, validation.Required),
		),
		validation.Field(
			&s.Command,
			validation.When(s.Spec == "" && s.Transport == MCPTransportStdio, validation.Required),
		),
		validation.Field(&s.Origin, validation.When(
			s.Transport == MCPTransportReverse,
			validation.Required,
			validation.By(func(value any) error {
				v, _ := value.(string)
				_, err := NormalizeOrigin(v)
				return err
			}),
		)),
		validation.Field(
			&s.Identity,
			validation.WithContext(func(ctx context.Context, value any) error {
				if s.Transport != MCPTransportReverse {
					return nil
				}
				edge, _ := ctx.Value(edgeContextKey{}).(EdgeConfig)
				if edge.WithDefaults().IsStaticPairing() {
					return nil
				}
				v, _ := value.(string)
				if v == "" {
					return fmt.Errorf(
						"identity is required for reverse transport unless edge.pairing.type is static",
					)
				}
				identities, _ := ctx.Value(identitiesContextKey{}).(map[string]*IdentityProfile)
				if _, ok := identities[v]; !ok {
					return fmt.Errorf("identity %q is not defined in identities", v)
				}
				return nil
			}),
		),
		// ランタイムの transport 選択（httpClientRoundTripper）は AuthValue > OAuth2 >
		// TokenExchange の優先順位で暗黙に1つだけを採用してしまうため、複数同時設定を
		// 設定ロード時点でエラーにする。いずれか1つ、または設定無しのみを許可する。
		validation.Field(&s.AuthValue, validation.By(func(value any) error {
			count := 0
			if s.AuthValue != nil {
				count++
			}
			if s.OAuth2 != nil {
				count++
			}
			if s.TokenExchange != nil {
				count++
			}
			if count > 1 {
				return fmt.Errorf("only one of authValue, oauth2, tokenExchange may be configured")
			}
			return nil
		})),
		validation.Field(&s.OAuth2),
		validation.Field(&s.TokenExchange),
		validation.Field(&s.SpecRefreshInterval, validation.By(func(value any) error {
			if s.SpecRefreshInterval != nil && *s.SpecRefreshInterval < 0 {
				return fmt.Errorf("must be zero or a positive duration")
			}
			return nil
		})),
		validation.Field(&s.Tools, validation.By(s.validateToolsFile)),
	)
}

// validateToolsFile implements the tools.file rules from the design memo
// (「config」節): it requires spec to be set (so MCP backend / reverse
// servers, which have Spec == "", are rejected), rejects a URL (local paths
// only), and is mutually exclusive with a positive specRefreshInterval.
// Split out of ValidateWithContext to keep that function's branching down.
func (s Server) validateToolsFile(value any) error {
	file := s.GeneratedToolsFile()
	if file == "" {
		return nil
	}
	// spec 未指定（MCP バックエンド／reverse）は Spec == "" で弾く。
	// 生成物には source.spec として記録するため、生成元が config に必要。
	if s.Spec == "" {
		return fmt.Errorf("tools.file requires spec to be set")
	}
	if strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") {
		return fmt.Errorf("tools.file must be a local path, not a URL")
	}
	if s.SpecRefreshInterval != nil && *s.SpecRefreshInterval > 0 {
		return fmt.Errorf("tools.file and specRefreshInterval are mutually exclusive")
	}
	return nil
}

// IsMCPBackend はこの Server が MCP バックエンドモードかどうかを返す。
// Spec が空で Transport が指定されている場合に MCP バックエンドモードとなる。
// reverse は別経路（エッジレジストリ）で扱うため除く。
func (s *Server) IsMCPBackend() bool {
	return s.Spec == "" && s.Transport != "" && s.Transport != MCPTransportReverse
}

// IsReverseBackend はこの Server が WebMCP reverse connection gateway 経由かどうかを返す。
func (s *Server) IsReverseBackend() bool {
	return s.Transport == MCPTransportReverse
}

// ToolsConfig groups static tool catalog (生成物) settings under
// mcpServers.<name>.tools. File is the only field in Phase 1; it is an
// object (rather than tools.file directly) so overrides (exclude/rename/
// description) can be added alongside it later without a breaking change.
type ToolsConfig struct {
	File string `mapstructure:"file"`
}

type OAuth2 struct {
	ClientID     string   `mapstructure:"clientID"`
	ClientSecret string   `mapstructure:"clientSecret"`
	AuthURL      string   `mapstructure:"authURL"`
	TokenURL     string   `mapstructure:"tokenURL"`
	Scopes       []string `mapstructure:"scopes"`
}

func (c *OAuth2) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, c,
		validation.Field(&c.ClientID, validation.Required),
		validation.Field(&c.ClientSecret, validation.Required),
		// is.RequestURI はスキーム無しの相対パス（例: "/auth"）も許容してしまうため、
		// スキーム付きの絶対 URL を要求する is.RequestURL を使う（TokenExchange.URL と同様）。
		validation.Field(&c.AuthURL, validation.Required, is.RequestURL),
		validation.Field(&c.TokenURL, validation.Required, is.RequestURL),
	)
}

type AuthValue struct {
	Header string `mapstructure:"header"`
	Prefix string `mapstructure:"prefix"`
	Value  string `mapstructure:"value"`
}

type TokenExchange struct {
	URL string `mapstructure:"url"`
}

func (c *TokenExchange) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, c,
		// is.RequestURI はスキーム無しの相対パス（例: "/token"）も許容してしまうため、
		// スキーム付きの絶対 URL を要求する is.RequestURL を使う。
		validation.Field(&c.URL, validation.Required, is.RequestURL),
	)
}
