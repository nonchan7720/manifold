package config

import (
	"context"
	"fmt"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

// DefaultAgentCardPath is the well-known Agent Card location fetched when
// Agent.AgentCardPath is unset.
const DefaultAgentCardPath = "/.well-known/agent-card.json"

// Agents is the top-level agents directive: A2A (Agent2Agent) agents exposed
// through the gateway, keyed by the name used in the /mcp/{name} path. The
// names share one namespace with mcpServers.
type Agents map[string]*Agent

// Agent configures one A2A agent. The gateway fetches its Agent Card from
// URL (+ AgentCardPath) and sends messages to the endpoint the card declares;
// URL itself is never used as the message endpoint.
type Agent struct {
	Name string

	// Description is an instruction for the calling agent: how and when to
	// use this agent. It is prepended to every skill's tool description and
	// returned as the agent's description in /mcp/list.
	Description string `mapstructure:"description"`

	// URL is the base URL the Agent Card is resolved from.
	URL string `mapstructure:"url"`
	// AgentCardPath overrides the Agent Card path relative to URL.
	AgentCardPath string `mapstructure:"agentCardPath"`

	ExtraHeaders map[string]string `mapstructure:"headers"`

	AuthValue     *AuthValue     `mapstructure:"authValue"`
	OAuth2        *OAuth2        `mapstructure:"oauth2"`
	TokenExchange *TokenExchange `mapstructure:"tokenExchange"`

	// Timeout bounds one message/send round trip; unset/0 uses
	// DefaultCallTimeout.
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

// validateSingleAuth rejects more than one of authValue/oauth2/tokenExchange:
// httpClientRoundTripper silently picks one by priority otherwise.
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

// Server converts the agent into the Server entry registered under
// mcpServers after validation, so every path keyed by server name (JWT
// pass-through, OAuth endpoints, header forwarding, authz, /mcp/list) treats
// an agent like any other backend. Transport is MCPTransportA2A.
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

// AgentCardPathOrDefault returns agentCardPath, falling back to
// DefaultAgentCardPath.
func (s Server) AgentCardPathOrDefault() string {
	if s.AgentCardPath == "" {
		return DefaultAgentCardPath
	}
	return s.AgentCardPath
}

// mergeAgentsIntoServers registers every agent as a Server under its name.
// Callers validate the Config first, which guarantees the names do not
// collide with mcpServers.
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
