package config

import (
	"context"
	"fmt"
	"slices"
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

// IsOpenAPI reports whether this server is in OpenAPI mode: either a live
// spec is configured, or tools.file is set so the gateway builds the catalog
// from the generated file instead (spec is then only needed to (re)generate
// that file, not to start the gateway).
func (s Server) IsOpenAPI() bool {
	return s.Spec != "" || s.GeneratedToolsFile() != ""
}

// EffectiveSpecRefreshInterval returns the refresh interval for this server,
// falling back to the gateway-wide default. Only a server with a live spec
// refreshes; others (MCP backend, reverse, and tools.file-only servers, which
// start from the generated file and never fetch a live spec) always return 0,
// regardless of the gateway-wide default.
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
		validation.Field(&s.BaseURL, validation.When(s.IsOpenAPI(), validation.Required)),
		validation.Field(
			&s.Transport,
			validation.When(
				!s.IsOpenAPI(),
				validation.In(MCPTransportHTTP, MCPTransportStdio, MCPTransportReverse),
			),
			validation.By(func(value any) error {
				if s.Transport != MCPTransportReverse {
					return nil
				}
				switch {
				case s.Spec != "":
					return fmt.Errorf("reverse transport does not support spec (OpenAPI mode)")
				case s.GeneratedToolsFile() != "":
					return fmt.Errorf(
						"reverse transport does not support tools.file (OpenAPI mode)",
					)
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
			validation.When(
				!s.IsOpenAPI() && s.Transport == MCPTransportHTTP, validation.Required,
			),
		),
		validation.Field(
			&s.Command,
			validation.When(
				!s.IsOpenAPI() && s.Transport == MCPTransportStdio, validation.Required,
			),
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
// (「config」節): spec is no longer required — tools.file alone puts the
// server in OpenAPI mode (see IsOpenAPI) and the gateway never reads spec
// when tools.file is present; spec is only needed to (re)generate the file
// via "manifold openapi generate" (enforced in pkg/cmd/openapi.go, not
// here). tools.file is mutually exclusive with transport/url/command (an MCP
// backend or reverse server must not also carry tools.file), must be a local
// path (not a URL), and is mutually exclusive with a positive
// specRefreshInterval. Split out of ValidateWithContext to keep that
// function's branching down.
func (s Server) validateToolsFile(value any) error {
	file := s.GeneratedToolsFile()
	if file == "" {
		return nil
	}
	switch {
	case s.Transport != "":
		return fmt.Errorf("tools.file does not support transport (OpenAPI mode)")
	case s.URL != "":
		return fmt.Errorf("tools.file does not support url (OpenAPI mode)")
	case s.Command != "":
		return fmt.Errorf("tools.file does not support command (OpenAPI mode)")
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
// OpenAPI モード（spec または tools.file のいずれかが設定済み）でなく、かつ
// Transport が指定されている場合に MCP バックエンドモードとなる。
// reverse は別経路（エッジレジストリ）で扱うため除く。
func (s *Server) IsMCPBackend() bool {
	return !s.IsOpenAPI() && s.Transport != "" && s.Transport != MCPTransportReverse
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

// OAuth2.UnknownClient が取る値。clients にマッピングの無い下流クライアントを
// 拒否するか、共用クライアントで上流へ進めるかを決める。
const (
	OAuth2UnknownClientReject  = "reject"
	OAuth2UnknownClientDefault = "default"
)

// oauth2ReservedAuthParams は認可リクエストの組み立てで Manifold 自身が
// 設定するパラメータ。authParams で上書きすると認可フローが壊れる。
var oauth2ReservedAuthParams = []string{
	"client_id",
	"redirect_uri",
	"response_type",
	"scope",
	"state",
	"code_challenge",
	"code_challenge_method",
}

// OAuth2Client は 1 つの下流クライアントに割り当てる上流 OAuth2 クライアント。
// 下流 client_id は map のキーではなくフィールドとして持つ。設定ローダーは
// map キーを小文字化し、さらに環境変数展開時にドットで分割するため、
// キーの位置では client_id を完全一致で保持できない。
type OAuth2Client struct {
	DownstreamClientID string `mapstructure:"downstreamClientID"`
	ClientID           string `mapstructure:"clientID"`
	ClientSecret       string `mapstructure:"clientSecret"`
}

type OAuth2 struct {
	ClientID     string   `mapstructure:"clientID"`
	ClientSecret string   `mapstructure:"clientSecret"`
	AuthURL      string   `mapstructure:"authURL"`
	TokenURL     string   `mapstructure:"tokenURL"`
	Scopes       []string `mapstructure:"scopes"`

	// Clients は下流 client_id（CIMD の URL または DCR で発行した ID）から
	// 上流クライアントへのマッピング。downstreamClientID で完全一致照合する。
	Clients []OAuth2Client `mapstructure:"clients"`

	// UnknownClient は Clients にマッピングの無い下流クライアントの扱い。
	// 空のときは UnknownClientMode を参照。
	UnknownClient string `mapstructure:"unknownClient"`

	// AuthParams は上流の認可リクエストに追加するクエリパラメータ。
	AuthParams map[string]string `mapstructure:"authParams"`
}

// UpstreamClient は下流 client_id に完全一致するマッピングを返す。
// 照合は正規化せずバイト単位で行う。
func (c *OAuth2) UpstreamClient(downstreamClientID string) (OAuth2Client, bool) {
	for _, client := range c.Clients {
		if client.DownstreamClientID == downstreamClientID {
			return client, true
		}
	}
	return OAuth2Client{}, false
}

// UnknownClientMode は Clients にマッピングが無い下流クライアントの扱いを返す。
// 明示指定が無い場合、Clients による whitelist があれば拒否し、無ければ
// 共用クライアント（clientID / clientSecret）で上流へ進む。
func (c *OAuth2) UnknownClientMode() string {
	if c.UnknownClient != "" {
		return c.UnknownClient
	}
	if len(c.Clients) > 0 {
		return OAuth2UnknownClientReject
	}
	return OAuth2UnknownClientDefault
}

func (c *OAuth2) ValidateWithContext(ctx context.Context) error {
	// 共用クライアントは、マッピングに無い下流クライアントを受け入れるときだけ必須。
	sharedRequired := c.UnknownClientMode() == OAuth2UnknownClientDefault
	return validation.ValidateStructWithContext(ctx, c,
		validation.Field(&c.ClientID, validation.When(sharedRequired, validation.Required)),
		validation.Field(&c.ClientSecret, validation.When(sharedRequired, validation.Required)),
		// is.RequestURI はスキーム無しの相対パス（例: "/auth"）も許容してしまうため、
		// スキーム付きの絶対 URL を要求する is.RequestURL を使う（TokenExchange.URL と同様）。
		validation.Field(&c.AuthURL, validation.Required, is.RequestURL),
		validation.Field(&c.TokenURL, validation.Required, is.RequestURL),
		validation.Field(&c.UnknownClient, validation.In(
			OAuth2UnknownClientReject, OAuth2UnknownClientDefault,
		)),
		validation.Field(&c.Clients, validation.By(validateOAuth2Clients)),
		validation.Field(&c.AuthParams, validation.By(validateOAuth2AuthParams)),
	)
}

func validateOAuth2Clients(value any) error {
	clients, ok := value.([]OAuth2Client)
	if !ok {
		return fmt.Errorf("type error: %T", value)
	}
	seen := make(map[string]struct{}, len(clients))
	for _, client := range clients {
		if client.DownstreamClientID == "" {
			return fmt.Errorf("downstreamClientID must not be empty")
		}
		if client.ClientID == "" {
			return fmt.Errorf("clientID for %q must not be empty", client.DownstreamClientID)
		}
		if _, dup := seen[client.DownstreamClientID]; dup {
			return fmt.Errorf("downstreamClientID %q is listed more than once",
				client.DownstreamClientID)
		}
		seen[client.DownstreamClientID] = struct{}{}
	}
	return nil
}

func validateOAuth2AuthParams(value any) error {
	params, ok := value.(map[string]string)
	if !ok {
		return fmt.Errorf("type error: %T", value)
	}
	for key := range params {
		if key == "" {
			return fmt.Errorf("parameter name must not be empty")
		}
		if slices.Contains(oauth2ReservedAuthParams, key) {
			return fmt.Errorf("parameter %q is set by manifold and must not be overridden", key)
		}
	}
	return nil
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
