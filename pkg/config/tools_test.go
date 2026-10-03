package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestToolsConfig_Allowed(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ToolsConfig
		tool string
		want bool
	}{
		{name: "nil allows everything", cfg: nil, tool: "anything", want: true},
		{name: "no patterns", cfg: &ToolsConfig{}, tool: "anything", want: true},
		{
			name: "include match",
			cfg:  &ToolsConfig{Include: []string{"get*"}},
			tool: "getpet",
			want: true,
		},
		{
			name: "include miss",
			cfg:  &ToolsConfig{Include: []string{"get*"}},
			tool: "addpet",
			want: false,
		},
		{
			name: "exclude match",
			cfg:  &ToolsConfig{Exclude: []string{"delete*"}},
			tool: "deletepet",
			want: false,
		},
		{
			name: "exclude wins over include",
			cfg:  &ToolsConfig{Include: []string{"*pet"}, Exclude: []string{"delete*"}},
			tool: "deletepet",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.cfg.Allowed(tt.tool))
		})
	}
}

func TestToolsConfig_ResolvedOverrides_UsesToolField(t *testing.T) {
	cfg := &ToolsConfig{Overrides: map[string]ToolOverride{
		"getpet":   {Name: "get_pet"},
		"listdocs": {Tool: "listDocs", Name: "list_docs"},
	}}
	got := cfg.ResolvedOverrides()
	require.Equal(t, "get_pet", got["getpet"].Name)
	require.Equal(t, "list_docs", got["listDocs"].Name)
	require.NotContains(t, got, "listdocs")
}

func TestToolsConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ToolsConfig
		wantErr string
	}{
		{name: "valid", cfg: ToolsConfig{
			Include:   []string{"get*"},
			Overrides: map[string]ToolOverride{"getpet": {Name: "get_pet"}},
		}},
		{
			name:    "bad pattern",
			cfg:     ToolsConfig{Include: []string{"["}},
			wantErr: "invalid tool pattern",
		},
		{
			name:    "bad name",
			cfg:     ToolsConfig{Overrides: map[string]ToolOverride{"a": {Name: "has space"}}},
			wantErr: "must be 1-128 characters",
		},
		{
			name: "duplicate rename",
			cfg: ToolsConfig{Overrides: map[string]ToolOverride{
				"a": {Name: "same"},
				"b": {Name: "same"},
			}},
			wantErr: `both rename to "same"`,
		},
		{
			name: "duplicate target",
			cfg: ToolsConfig{Overrides: map[string]ToolOverride{
				"a": {Tool: "x", Name: "one"},
				"b": {Tool: "x", Name: "two"},
			}},
			wantErr: `both target tool "x"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.ValidateWithContext(t.Context())
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestCacheConfig(t *testing.T) {
	cfg := &CacheConfig{ToolsList: time.Minute, ToolCall: time.Minute, Tools: []string{"get*"}}
	require.NoError(t, cfg.ValidateWithContext(t.Context()))
	require.True(t, cfg.CachesToolsList())
	require.True(t, cfg.CachesToolCall("getpet"))
	require.False(t, cfg.CachesToolCall("addpet"))

	var nilCfg *CacheConfig
	require.False(t, nilCfg.CachesToolsList())
	require.False(t, nilCfg.CachesToolCall("getpet"))

	err := CacheConfig{ToolCall: time.Minute}.ValidateWithContext(t.Context())
	require.ErrorContains(t, err, "cache.tools is required")
	err = CacheConfig{ToolsList: -time.Second}.ValidateWithContext(t.Context())
	require.ErrorContains(t, err, "zero or positive")
}

func TestToolSearchConfig_Defaults(t *testing.T) {
	var nilCfg *ToolSearchConfig
	require.False(t, nilCfg.IsEnabled())
	require.Equal(t, DefaultToolSearchMaxResults, nilCfg.MaxResultsOrDefault())
	require.Equal(t, 3, (&ToolSearchConfig{Enabled: true, MaxResults: 3}).MaxResultsOrDefault())
	require.Error(t, ToolSearchConfig{MaxResults: -1}.ValidateWithContext(t.Context()))
}

func TestLoadInternal_ToolSettings(t *testing.T) {
	cfg, err := loadFromYAML(t, `
mcpServers:
  petstore:
    description: Swagger Petstore
    spec: https://petstore3.swagger.io/api/v3/openapi.json
    tools:
      include: ["get*", "find*"]
      exclude: ["*inventory*"]
      overrides:
        getpetbyid:
          name: get_pet
          description: Look up a pet
        mixed:
          tool: ListDocs
          name: list_docs
    toolSearch:
      enabled: true
      maxResults: 5
    cache:
      toolsList: 1m
      toolCall: 30s
      tools: ["get*"]
audit:
  enabled: true
  output: stdout
  includeArguments: true
`)
	require.NoError(t, err)
	srv := cfg.MCPServer["petstore"]
	require.Equal(t, []string{"get*", "find*"}, srv.Tools.Include)
	require.Equal(t, []string{"*inventory*"}, srv.Tools.Exclude)
	overrides := srv.Tools.ResolvedOverrides()
	require.Equal(t, "get_pet", overrides["getpetbyid"].Name)
	require.Equal(t, "Look up a pet", overrides["getpetbyid"].Description)
	require.Equal(t, "list_docs", overrides["ListDocs"].Name)
	require.True(t, srv.ToolSearch.IsEnabled())
	require.Equal(t, 5, srv.ToolSearch.MaxResultsOrDefault())
	require.Equal(t, time.Minute, srv.Cache.ToolsList)
	require.Equal(t, 30*time.Second, srv.Cache.ToolCall)
	require.True(t, cfg.Audit.Enabled)
	require.Equal(t, AuditOutputStdout, cfg.Audit.OutputOrDefault())
	require.True(t, cfg.Audit.IncludeArguments)
	// encryptKey も store も無い最小構成はインメモリ + 生成した鍵で通る
	require.True(t, cfg.UsesEphemeralStore())
	require.NotEmpty(t, cfg.Gateway.EncryptKey)
}

func TestServer_ValidateWithContext_SpecWithoutBaseURL_IsValid(t *testing.T) {
	s := Server{Description: "d", Spec: "https://example.com/openapi.json"}
	require.NoError(t, s.ValidateWithContext(t.Context()))
}

func TestServer_ValidateWithContext_ReverseToolSearch_Invalid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{
		"r": {
			Description: "r",
			Transport:   MCPTransportReverse,
			Origin:      "https://app.example.com",
			ToolSearch:  &ToolSearchConfig{Enabled: true},
		},
	})
	cfg.Gateway.Edge.Pairing.Type = "static"
	err := cfg.ValidateWithContext(t.Context())
	require.ErrorContains(t, err, "toolSearch is not supported")
}

func TestAuditConfig_OutputOrDefault(t *testing.T) {
	require.Equal(t, AuditOutputStderr, AuditConfig{}.OutputOrDefault())
	require.Equal(
		t,
		"/var/log/audit.jsonl",
		AuditConfig{Output: "/var/log/audit.jsonl"}.OutputOrDefault(),
	)
	require.Error(t, AuditConfig{Output: "  "}.ValidateWithContext(t.Context()))
}
