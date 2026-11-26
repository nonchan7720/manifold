package config

import (
	"strings"
	"testing"

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

func TestToolsConfig_Excluded(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ToolsConfig
		tool string
		want bool
	}{
		{name: "nil excludes nothing", cfg: nil, tool: "deletePet", want: false},
		{name: "no patterns", cfg: &ToolsConfig{}, tool: "deletePet", want: false},
		{
			name: "exact match",
			cfg:  &ToolsConfig{Exclude: []string{"deletePet"}},
			tool: "deletePet",
			want: true,
		},
		{
			name: "glob match",
			cfg:  &ToolsConfig{Exclude: []string{"delete*"}},
			tool: "deleteOrder",
			want: true,
		},
		{
			name: "case-sensitive",
			cfg:  &ToolsConfig{Exclude: []string{"deletepet"}},
			tool: "deletePet",
			want: false,
		},
		{
			name: "include is not consulted",
			cfg:  &ToolsConfig{Include: []string{"get*"}},
			tool: "deletePet",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.cfg.Excluded(tt.tool))
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

func TestLoadInternal_ToolSettings(t *testing.T) {
	cfg, err := loadFromYAML(t, `
mcpServers:
  petstore:
    description: Swagger Petstore
    spec: https://petstore3.swagger.io/api/v3/openapi.json
    tools:
      include: ["get*", "find*"]
      exclude: ["*inventory*", "deletePet"]
      overrides:
        getpetbyid:
          name: get_pet
          description: Look up a pet
        mixed:
          tool: ListDocs
          name: list_docs
audit:
  enabled: true
  output: stdout
  includeArguments: true
`)
	require.NoError(t, err)
	srv := cfg.MCPServer["petstore"]
	require.Equal(t, []string{"get*", "find*"}, srv.Tools.Include)
	// パターンは大文字小文字を保つ（OpenAPI では operationId にも照合する）
	require.Equal(t, []string{"*inventory*", "deletePet"}, srv.Tools.Exclude)
	overrides := srv.Tools.ResolvedOverrides()
	require.Equal(t, "get_pet", overrides["getpetbyid"].Name)
	require.Equal(t, "Look up a pet", overrides["getpetbyid"].Description)
	require.Equal(t, "list_docs", overrides["ListDocs"].Name)
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

func TestResultFilterConfig_Applies(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ResultFilterConfig
		tool string
		want bool
	}{
		{"nil config", nil, "list_issues", false},
		{"disabled", &ResultFilterConfig{Include: []string{"*"}}, "list_issues", false},
		{"enabled without patterns", &ResultFilterConfig{Enabled: true}, "list_issues", true},
		{"included", &ResultFilterConfig{Enabled: true, Include: []string{"list_*"}},
			"list_issues", true},
		{"not included", &ResultFilterConfig{Enabled: true, Include: []string{"list_*"}},
			"get_issue", false},
		{"excluded", &ResultFilterConfig{Enabled: true, Exclude: []string{"*_file"}},
			"get_file", false},
		{"exclude wins over include", &ResultFilterConfig{
			Enabled: true, Include: []string{"get_*"}, Exclude: []string{"*_file"},
		}, "get_file", false},
		{"patterns are case-sensitive", &ResultFilterConfig{
			Enabled: true, Include: []string{"getPet"},
		}, "getpet", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.cfg.Applies(tt.tool))
		})
	}
}

func TestResultFilterConfig_ArgumentName(t *testing.T) {
	var unset *ResultFilterConfig
	require.Equal(t, "_jmespath", unset.ArgumentName())
	require.Equal(t, "_jmespath", (&ResultFilterConfig{}).ArgumentName())
	require.Equal(t, "_query", (&ResultFilterConfig{Argument: "_query"}).ArgumentName())
}

func TestToolsConfig_Validate_ResultFilter(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ResultFilterConfig
		wantErr string
	}{
		{name: "defaults", cfg: ResultFilterConfig{Enabled: true}},
		{name: "custom argument and patterns", cfg: ResultFilterConfig{
			Enabled: true, Argument: "result.query-1", Include: []string{"list_*"},
			Exclude: []string{"*_raw"},
		}},
		{
			name:    "argument with a space",
			cfg:     ResultFilterConfig{Enabled: true, Argument: "my query"},
			wantErr: `resultFilter: argument "my query" must be 1-64 characters`,
		},
		{
			name:    "argument too long",
			cfg:     ResultFilterConfig{Argument: strings.Repeat("a", 65)},
			wantErr: "must be 1-64 characters",
		},
		{
			name:    "bad include pattern",
			cfg:     ResultFilterConfig{Enabled: true, Include: []string{"["}},
			wantErr: `resultFilter: invalid tool pattern "["`,
		},
		{
			name:    "bad exclude pattern",
			cfg:     ResultFilterConfig{Enabled: true, Exclude: []string{"a[b"}},
			wantErr: `resultFilter: invalid tool pattern "a[b"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ToolsConfig{ResultFilter: &tt.cfg}.ValidateWithContext(t.Context())
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestLoadInternal_ResultFilter(t *testing.T) {
	cfg, err := loadFromYAML(t, `
mcpServers:
  github:
    description: GitHub
    transport: http
    url: https://example.com/mcp
    tools:
      resultFilter:
        enabled: true
        argument: _query
        include: ["list_*", "getIssue"]
        exclude: ["list_secrets"]
  petstore:
    description: Petstore
    spec: https://petstore3.swagger.io/api/v3/openapi.json
    tools:
      include: ["get*"]
  defaults:
    description: Defaults
    spec: https://petstore3.swagger.io/api/v3/openapi.json
    tools:
      resultFilter:
        enabled: true
`)
	require.NoError(t, err)

	github := cfg.MCPServer["github"].Tools.ResultFilter
	require.Equal(t, &ResultFilterConfig{
		Enabled:  true,
		Argument: "_query",
		// パターンは値なので、viper がキーを小文字化しても表記は変わらない。
		Include: []string{"list_*", "getIssue"},
		Exclude: []string{"list_secrets"},
	}, github)
	require.True(t, github.Applies("getIssue"))
	require.False(t, github.Applies("list_secrets"))

	// 設定しなければ無効のまま（既存の設定の挙動は変わらない）。
	require.Nil(t, cfg.MCPServer["petstore"].Tools.ResultFilter)
	require.False(t, cfg.MCPServer["petstore"].Tools.ResultFilter.Applies("getpet"))

	defaults := cfg.MCPServer["defaults"].Tools.ResultFilter
	require.Equal(t, "_jmespath", defaults.ArgumentName())
	require.True(t, defaults.Applies("anything"))
}

func TestLoadInternal_ResultFilter_InvalidArgument(t *testing.T) {
	_, err := loadFromYAML(t, `
mcpServers:
  github:
    description: GitHub
    transport: http
    url: https://example.com/mcp
    tools:
      resultFilter:
        enabled: true
        argument: "has space"
`)
	require.ErrorContains(t, err, `argument "has space" must be 1-64 characters`)
}
