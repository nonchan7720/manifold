package config

import (
	"context"
	"fmt"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

type MCPTransport string

const (
	MCPTransportHTTP  MCPTransport = "http"
	MCPTransportStdio MCPTransport = "stdio"
)

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
}

func (s Server) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(
		ctx,
		&s,
		validation.Field(&s.Description, validation.Required),
		validation.Field(&s.BaseURL, validation.When(s.Spec != "", validation.Required)),
		validation.Field(
			&s.Transport,
			validation.When(s.Spec == "", validation.In(MCPTransportHTTP, MCPTransportStdio)),
		),
		validation.Field(
			&s.URL,
			validation.When(s.Spec == "" && s.Transport == MCPTransportHTTP, validation.Required),
		),
		validation.Field(
			&s.Command,
			validation.When(s.Spec == "" && s.Transport == MCPTransportStdio, validation.Required),
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
func (s *Server) IsMCPBackend() bool {
	return s.Spec == "" && s.Transport != ""
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
