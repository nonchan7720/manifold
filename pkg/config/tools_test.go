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
	require.Equal(t, time.Minute, srv.Cache.ToolsList)
	require.Equal(t, 30*time.Second, srv.Cache.ToolCall)
	require.True(t, cfg.Audit.Enabled)
	require.Equal(t, AuditOutputStdout, cfg.Audit.OutputOrDefault())
	require.True(t, cfg.Audit.IncludeArguments)
	// encryptKey も store も無い最小構成はインメモリなのでロード時は通り、
	// 鍵はロード時には生成されない（gateway 起動時に ApplyEphemeralDefaults が生成する）
	require.True(t, cfg.UsesEphemeralStore())
	require.Empty(t, cfg.Gateway.EncryptKey)
}

func TestServer_ValidateWithContext_SpecWithoutBaseURL_IsValid(t *testing.T) {
	s := Server{Description: "d", Spec: "https://example.com/openapi.json"}
	require.NoError(t, s.ValidateWithContext(t.Context()))
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

// baseURL は spec があれば省略できるが、キーを明示した空値（未設定の環境変数の
// 展開結果など）は導出に回さずエラーにする。
func TestLoadInternal_BaseURL_AbsentVsEmpty(t *testing.T) {
	load := func(t *testing.T, baseURL string) (*Config, error) {
		t.Helper()
		return loadFromYAML(t, `
mcpServers:
  api:
    description: api
    spec: https://example.com/openapi.json
`+baseURL)
	}

	t.Run("absent with spec is derived from the spec", func(t *testing.T) {
		cfg, err := load(t, "")
		require.NoError(t, err)
		require.False(t, cfg.MCPServer["api"].BaseURLSet)
	})
	t.Run("present but empty is an error", func(t *testing.T) {
		_, err := load(t, `    baseURL: ""`)
		require.ErrorContains(t, err, "BaseURL: cannot be blank")
	})
	t.Run("unset env var expanding to empty is an error", func(t *testing.T) {
		t.Setenv("TEST_BASEURL_UNSET", "")
		_, err := load(t, `    baseURL: ${TEST_BASEURL_UNSET}`)
		require.ErrorContains(t, err, "BaseURL: cannot be blank")
	})
	t.Run("present non-empty is valid", func(t *testing.T) {
		cfg, err := load(t, `    baseURL: https://api.example.com`)
		require.NoError(t, err)
		require.True(t, cfg.MCPServer["api"].BaseURLSet)
		require.Equal(t, "https://api.example.com", cfg.MCPServer["api"].BaseURL)
	})
}

// expandEnvVars は展開した値を viper の override 層に書くため、mcpServers 配下の
// どこかで ${VAR} が展開されると v.Get("mcpServers") は override 層の部分木だけを
// 返す。baseURL キーの有無はその影響を受けずに判定できなければならない。
func TestLoadInternal_BaseURL_EmptyIsRejectedWhenAnotherKeyExpandsEnvVar(t *testing.T) {
	t.Setenv("TEST_BASEURL_TOKEN", "secret")
	t.Setenv("TEST_BASEURL_DESC", "expanded")

	t.Run("env var in another server", func(t *testing.T) {
		_, err := loadFromYAML(t, `
mcpServers:
  api:
    description: api
    spec: https://example.com/openapi.json
    baseURL: ""
  other:
    description: other
    spec: https://example.com/other.json
    baseURL: https://other.example.com
    headers:
      Authorization: ${TEST_BASEURL_TOKEN}
`)
		require.ErrorContains(t, err, "api: (BaseURL: cannot be blank")
	})
	t.Run("env var in the same server", func(t *testing.T) {
		_, err := loadFromYAML(t, `
mcpServers:
  api:
    description: ${TEST_BASEURL_DESC}
    spec: https://example.com/openapi.json
    baseURL: ""
`)
		require.ErrorContains(t, err, "api: (BaseURL: cannot be blank")
	})
	t.Run("absent baseURL next to an expanded key is still derived", func(t *testing.T) {
		cfg, err := loadFromYAML(t, `
mcpServers:
  api:
    description: api
    spec: https://example.com/openapi.json
    headers:
      Authorization: ${TEST_BASEURL_TOKEN}
`)
		require.NoError(t, err)
		require.False(t, cfg.MCPServer["api"].BaseURLSet)
		// viper は map のキーを小文字化する
		require.Equal(t, "secret", cfg.MCPServer["api"].ExtraHeaders["authorization"])
	})
}

func TestServer_ValidateWithContext_SpecWithEmptyBaseURLSet_IsInvalid(t *testing.T) {
	s := Server{Description: "d", Spec: "https://example.com/openapi.json", BaseURLSet: true}
	require.Error(t, s.ValidateWithContext(t.Context()))
}
