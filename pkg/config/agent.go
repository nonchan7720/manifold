package config

import (
	"context"
	"fmt"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

// DefaultAgentCardPath は Agent.AgentCardPath 未設定時に取得する
// well-known の Agent Card パス。
const DefaultAgentCardPath = "/.well-known/agent-card.json"

// Agents はトップレベルの agents ディレクティブ。ゲートウェイ経由で公開する
// A2A（Agent2Agent）エージェントを、/mcp/{name} パスに使う名前をキーに持つ。
// 名前は mcpServers と同じ名前空間を共有する。
type Agents map[string]*Agent

// Agent は A2A エージェント 1 つの設定。ゲートウェイは URL（+ AgentCardPath）
// から Agent Card を取得し、メッセージは Card に書かれたエンドポイントへ送る。
// URL 自体をメッセージの送信先に使うことはない。
type Agent struct {
	Name string

	// Description は呼び出し元エージェントへの指示文（このエージェントを
	// いつ・どう使うか）。各スキルのツール description の先頭に付き、
	// /mcp/list ではエージェントの description として返される。
	Description string `mapstructure:"description"`

	// URL は Agent Card を取得するベース URL。
	URL string `mapstructure:"url"`
	// AgentCardPath は URL からの Agent Card のパスを上書きする。
	AgentCardPath string `mapstructure:"agentCardPath"`

	ExtraHeaders map[string]string `mapstructure:"headers"`

	AuthValue     *AuthValue     `mapstructure:"authValue"`
	OAuth2        *OAuth2        `mapstructure:"oauth2"`
	TokenExchange *TokenExchange `mapstructure:"tokenExchange"`

	// Timeout は message/send 1 回のタイムアウト。未設定/0 は DefaultCallTimeout。
	Timeout time.Duration `mapstructure:"timeout"`
}

func (a Agent) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(
		ctx,
		&a,
		validation.Field(&a.Description, validation.Required),
		validation.Field(&a.URL, validation.Required, is.RequestURL),
		validation.Field(&a.AuthValue, validation.By(func(any) error {
			return validateSingleAuth(a.AuthValue, a.OAuth2, a.TokenExchange)
		})),
		validation.Field(&a.OAuth2),
		validation.Field(&a.TokenExchange),
		validation.Field(&a.Timeout, validation.Min(time.Duration(0))),
	)
}

// validateSingleAuth は authValue/oauth2/tokenExchange の複数同時設定を拒否する。
// 許すと httpClientRoundTripper が優先順位で暗黙に 1 つだけを採用してしまうため。
func validateSingleAuth(auth *AuthValue, oauth2 *OAuth2, tokenExchange *TokenExchange) error {
	count := 0
	if auth != nil {
		count++
	}
	if oauth2 != nil {
		count++
	}
	if tokenExchange != nil {
		count++
	}
	if count > 1 {
		return fmt.Errorf("only one of authValue, oauth2, tokenExchange may be configured")
	}
	return nil
}

// Server はエージェントを、検証後に mcpServers 配下へ登録する Server へ変換する。
// これでサーバー名をキーにする経路（JWT パススルー、OAuth エンドポイント、
// ヘッダー転送、authz、/mcp/list）は他のバックエンドと同じようにエージェントを
// 扱える。Transport は MCPTransportA2A。
func (a *Agent) Server() *Server {
	return &Server{
		Name:          a.Name,
		Description:   a.Description,
		Transport:     MCPTransportA2A,
		URL:           a.URL,
		AgentCardPath: a.AgentCardPath,
		ExtraHeaders:  a.ExtraHeaders,
		AuthValue:     a.AuthValue,
		OAuth2:        a.OAuth2,
		TokenExchange: a.TokenExchange,
		CallTimeout:   a.Timeout,
	}
}

// AgentCardPathOrDefault は agentCardPath を返す。未設定なら DefaultAgentCardPath。
func (s Server) AgentCardPathOrDefault() string {
	if s.AgentCardPath == "" {
		return DefaultAgentCardPath
	}
	return s.AgentCardPath
}

// mergeAgentsIntoServers は各エージェントをその名前で Server として登録する。
// 呼び出し側が先に Config を検証するため、名前が mcpServers と衝突しないことは
// 保証されている。
func mergeAgentsIntoServers(servers Servers, agents Agents) Servers {
	if len(agents) == 0 {
		return servers
	}
	if servers == nil {
		servers = Servers{}
	}
	for name, agent := range agents {
		servers[name] = agent.Server()
	}
	return servers
}
