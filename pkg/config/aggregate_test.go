package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func aggregateTestServers() Servers {
	return Servers{
		"petstore": {Name: "petstore", Spec: "https://example.com/openapi.json"},
		"github":   {Name: "github", Transport: MCPTransportHTTP, URL: "https://example.com/mcp"},
		"login": {
			Name:   "login",
			Spec:   "https://example.com/openapi.json",
			OAuth2: &OAuth2{},
		},
		"browser": {Name: "browser", Transport: MCPTransportReverse},
	}
}

func TestAggregateConfig_Members(t *testing.T) {
	servers := aggregateTestServers()
	require.Equal(t, []string{"github", "petstore"}, AggregateConfig{}.Members(servers))
	require.Equal(t,
		[]string{"github", "petstore"},
		AggregateConfig{Servers: []string{"petstore", "github", "petstore"}}.Members(servers),
	)
}

func TestAggregateConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     AggregateConfig
		servers Servers
		wantErr string
	}{
		{name: "disabled is never checked", cfg: AggregateConfig{Servers: []string{"missing"}}},
		{name: "all eligible", cfg: AggregateConfig{Enabled: true}},
		{
			name:    "unknown server",
			cfg:     AggregateConfig{Enabled: true, Servers: []string{"missing"}},
			wantErr: `"missing" is not defined`,
		},
		{
			name:    "oauth2 server",
			cfg:     AggregateConfig{Enabled: true, Servers: []string{"login"}},
			wantErr: "oauth2 servers",
		},
		{
			name:    "reverse server",
			cfg:     AggregateConfig{Enabled: true, Servers: []string{"browser"}},
			wantErr: "reverse transport",
		},
		{
			name:    "bad separator",
			cfg:     AggregateConfig{Enabled: true, Separator: "/"},
			wantErr: "separator",
		},
		{
			name: "server name contains separator",
			cfg:  AggregateConfig{Enabled: true},
			servers: Servers{
				"my__api": {Name: "my__api", Spec: "https://example.com/openapi.json"},
			},
			wantErr: "must not contain the separator",
		},
		{
			name: "custom separator avoids the clash",
			cfg:  AggregateConfig{Enabled: true, Separator: "."},
			servers: Servers{
				"my__api": {Name: "my__api", Spec: "https://example.com/openapi.json"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			servers := tt.servers
			if servers == nil {
				servers = aggregateTestServers()
			}
			err := tt.cfg.ValidateServers(t.Context(), servers)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestLoadInternal_Aggregate(t *testing.T) {
	cfg, err := loadFromYAML(t, `
gateway:
  aggregate:
    enabled: true
    toolSearch:
      enabled: true
mcpServers:
  petstore:
    description: Swagger Petstore
    spec: https://petstore3.swagger.io/api/v3/openapi.json
agents:
  helper:
    description: helper agent
    url: https://agent.example.com
`)
	require.NoError(t, err)
	agg := cfg.Gateway.Aggregate
	require.True(t, agg.Enabled)
	require.True(t, agg.ToolSearch.IsEnabled())
	require.Equal(t, []string{"helper", "petstore"}, agg.Members(cfg.MCPServer))
}

func TestLoadInternal_Aggregate_UnknownServer(t *testing.T) {
	_, err := loadFromYAML(t, `
gateway:
  aggregate:
    enabled: true
    servers: [nope]
mcpServers:
  petstore:
    description: Swagger Petstore
    spec: https://petstore3.swagger.io/api/v3/openapi.json
`)
	require.ErrorContains(t, err, `"nope" is not defined`)
}
