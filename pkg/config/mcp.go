package config

import (
	"context"

	validation "github.com/go-ozzo/ozzo-validation/v4"
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
	AuthValue    *AuthValue        `mapstructure:"authValue"`
	OAuth2       *OAuth2           `mapstructure:"oauth2"`

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
		validation.Field(&s.Transport, validation.When(s.Spec == "", validation.In(MCPTransportHTTP, MCPTransportStdio))),
		validation.Field(&s.URL, validation.When(s.Spec == "" && s.Transport == MCPTransportHTTP, validation.Required)),
		validation.Field(&s.Command, validation.When(s.Spec == "" && s.Transport == MCPTransportStdio, validation.Required)),
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

type AuthValue struct {
	Header string `mapstructure:"header"`
	Prefix string `mapstructure:"prefix"`
	Value  string `mapstructure:"value"`
}
