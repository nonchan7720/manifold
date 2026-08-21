package config

import (
	"context"
	"fmt"
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
	)
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
