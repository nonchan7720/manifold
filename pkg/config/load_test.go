package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestFindProjectRoot(t *testing.T) {
	// テスト実行時のカレントディレクトリは pkg/config/ であるが、
	// findProjectRoot は go.mod が見つかるまで上に辿る。
	// このプロジェクトのルートに go.mod があるはず。
	root := findProjectRoot()

	// go.mod が存在することを確認
	goModPath := filepath.Join(root, "go.mod")
	_, err := os.Stat(goModPath)
	require.NoError(t, err, "go.mod should exist in project root: %s", root)

	// カレントディレクトリ配下であることを確認
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(cwd, root) || root == cwd,
		"cwd %s should be under or equal to root %s", cwd, root)
}

func TestFindProjectRoot_NotDot(t *testing.T) {
	root := findProjectRoot()
	// go.mod があるディレクトリが見つかれば "." でないはず
	require.NotEqual(t, ".", root)
}

func TestLoadInternal_Success(t *testing.T) {
	// config.yaml の mcpServers.google.oauth2 が必須にしているダミー値を設定する
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// プロジェクトに config.yaml があるので loadInternal は成功するはず
	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	// config.yaml に gateway.port: 9999 がある
	require.Equal(t, 9998, cfg.Gateway.Port)
}

// --- fileFetch ---

func TestLoadInternal_FileFetch_Defaults(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// プロジェクトの config.yaml に fileFetch セクションは無いので、既定値が適用される
	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, DefaultFileFetchMaxSize, cfg.FileFetch.MaxSize)
	require.False(t, cfg.FileFetch.AllowLocal)
	require.Empty(t, cfg.FileFetch.AllowedHosts)
}

func TestLoadInternal_FileFetch_EnvOverride_MaxSize(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// viper の SetDefault + AutomaticEnv により FILEFETCH_MAXSIZE で上書きできる
	t.Setenv("FILEFETCH_MAXSIZE", "1048576")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.EqualValues(t, 1048576, cfg.FileFetch.MaxSize)
}

func TestLoadInternal_FileFetch_EnvOverride_AllowLocal(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// viper の SetDefault + AutomaticEnv により FILEFETCH_ALLOWLOCAL で上書きできる
	t.Setenv("FILEFETCH_ALLOWLOCAL", "true")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.True(t, cfg.FileFetch.AllowLocal)
}

func TestLoadInternal_FileFetch_EnvOverride_AllowedHosts(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// viper の SetDefault + AutomaticEnv により FILEFETCH_ALLOWEDHOSTS で上書きできる。
	// カンマ区切りの文字列が viper 既定の StringToSliceHookFunc(",") で []string にデコードされる。
	t.Setenv("FILEFETCH_ALLOWEDHOSTS", "files.example.com,cdn.example.com")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, []string{"files.example.com", "cdn.example.com"}, cfg.FileFetch.AllowedHosts)
}

// --- authz ---

func TestLoadInternal_Authz_Defaults(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// プロジェクトの config.yaml に authz セクションは無いので、既定値が適用される
	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.False(t, cfg.Authz.Enabled)
	require.Equal(t, DefaultAuthzOPAURL, cfg.Authz.OPAURL)
	require.Equal(t, DefaultAuthzTimeout, cfg.Authz.Timeout)
	require.Equal(t, DefaultAuthzDecisionPathList, cfg.Authz.DecisionPath.List)
	require.Equal(t, DefaultAuthzDecisionPathCall, cfg.Authz.DecisionPath.Call)
	require.Equal(t, DefaultAuthzHeaderUserID, cfg.Authz.Headers.UserID)
	require.Equal(t, DefaultAuthzHeaderUserGroups, cfg.Authz.Headers.UserGroups)
	require.Equal(t, DefaultAuthzHeaderBypass, cfg.Authz.Headers.Bypass)
}

func TestLoadInternal_Authz_EnvOverride_Enabled(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// viper の SetDefault + AutomaticEnv により AUTHZ_ENABLED で上書きできる
	t.Setenv("AUTHZ_ENABLED", "true")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.True(t, cfg.Authz.Enabled)
}

func TestLoadInternal_Authz_EnvOverride_OPAURL(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// viper の SetDefault + AutomaticEnv により AUTHZ_OPAURL で上書きできる
	t.Setenv("AUTHZ_OPAURL", "https://opa-sidecar.internal.example.com:8181")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, "https://opa-sidecar.internal.example.com:8181", cfg.Authz.OPAURL)
}

func TestLoadInternal_Authz_EnvOverride_HeadersBypass(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// viper の SetDefault + AutomaticEnv により AUTHZ_HEADERS_BYPASS で上書きできる
	t.Setenv("AUTHZ_HEADERS_BYPASS", "x-acme-bypass")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, "x-acme-bypass", cfg.Authz.Headers.Bypass)
}

func TestLoadInternal_Authz_EnvOverride_Timeout(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// 回帰確認: 独自の DecodeHook を合成しても viper 既定の
	// StringToTimeDurationHookFunc が引き続き効いていること
	t.Setenv("AUTHZ_TIMEOUT", "5s")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, cfg.Authz.Timeout)
}

// --- telemetry: headers を JSON 文字列の環境変数から注入 ---

func TestConfigDecodeHook_HeadersFromJSONEnvVar(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS_JSON", `{"Authorization":"Bearer test-token"}`)

	const yamlDoc = `
telemetry:
  trace:
    http:
      headers: ${OTEL_EXPORTER_OTLP_HEADERS_JSON}
`
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yamlDoc)))
	require.NoError(t, expandEnvVars(v))

	var conf Config
	require.NoError(t, v.Unmarshal(&conf, viper.DecodeHook(configDecodeHook())))
	require.NotNil(t, conf.Telemetry.Trace.HTTP)
	require.Equal(
		t,
		map[string]string{"Authorization": "Bearer test-token"},
		conf.Telemetry.Trace.HTTP.Headers,
	)
}

func TestConfigDecodeHook_HeadersFromJSONEnvVar_UnsetIsEmpty(t *testing.T) {
	const yamlDoc = `
telemetry:
  trace:
    http:
      headers: ${OTEL_EXPORTER_OTLP_HEADERS_JSON}
`
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yamlDoc)))
	require.NoError(t, expandEnvVars(v))

	var conf Config
	require.NoError(t, v.Unmarshal(&conf, viper.DecodeHook(configDecodeHook())))
	require.Empty(t, conf.Telemetry.Trace.HTTP.Headers)
}

func TestConfigDecodeHook_HeadersFromJSONEnvVar_InvalidJSON(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS_JSON", `{"Authorization": invalid}`)

	const yamlDoc = `
telemetry:
  trace:
    http:
      headers: ${OTEL_EXPORTER_OTLP_HEADERS_JSON}
`
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yamlDoc)))
	require.NoError(t, expandEnvVars(v))

	var conf Config
	err := v.Unmarshal(&conf, viper.DecodeHook(configDecodeHook()))
	require.Error(t, err)
}

// --- reverse origin 正規化 ---

func TestNormalizeReverseOrigins_LowercasesAndTrimsSlash(t *testing.T) {
	servers := Servers{
		"app1": {Transport: MCPTransportReverse, Origin: "HTTPS://App1.Example.COM/"},
	}
	normalizeReverseOrigins(servers)
	require.Equal(t, "https://app1.example.com", servers["app1"].Origin)
}

func TestNormalizeReverseOrigins_LeavesInvalidOriginUntouched(t *testing.T) {
	// 不正な値の報告は Server.ValidateWithContext の責務なので、正規化に失敗しても
	// 元の値をそのまま残す。
	servers := Servers{
		"app1": {Transport: MCPTransportReverse, Origin: "not a url"},
	}
	normalizeReverseOrigins(servers)
	require.Equal(t, "not a url", servers["app1"].Origin)
}

func TestNormalizeReverseOrigins_IgnoresNonReverseServers(t *testing.T) {
	servers := Servers{
		"http-backend": {Transport: MCPTransportHTTP, URL: "http://example.com"},
	}
	normalizeReverseOrigins(servers)
	require.Equal(t, "http://example.com", servers["http-backend"].URL)
}

// --- oauth.cimd ---

func TestLoadInternal_OAuthCIMD_Defaults(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// プロジェクトの config.yaml に oauth セクションは無いので既定値が適用される
	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.False(t, cfg.OAuth.CIMD.Enabled)
	require.Equal(t, DefaultCIMDCacheTTL, cfg.OAuth.CIMD.CacheTTL)
	require.Equal(t, DefaultCIMDMaxDocumentSize, cfg.OAuth.CIMD.MaxDocumentSize)
	require.Empty(t, cfg.OAuth.CIMD.AllowedOrigins)
}

func TestLoadInternal_OAuthCIMD_EnvOverride(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	t.Setenv("OAUTH_CIMD_ENABLED", "true")
	t.Setenv("OAUTH_CIMD_CACHETTL", "5m")
	t.Setenv("OAUTH_CIMD_MAXDOCUMENTSIZE", "1024")
	t.Setenv("OAUTH_CIMD_ALLOWEDORIGINS", "https://a.example.com,https://b.example.com")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.True(t, cfg.OAuth.CIMD.Enabled)
	require.Equal(t, 5*time.Minute, cfg.OAuth.CIMD.CacheTTL)
	require.EqualValues(t, 1024, cfg.OAuth.CIMD.MaxDocumentSize)
	require.Equal(
		t,
		[]string{"https://a.example.com", "https://b.example.com"},
		cfg.OAuth.CIMD.AllowedOrigins,
	)
}

// --- mcpServers.<name>.oauth2.clients: 設定ファイル経由でキーが変質しないこと ---

// loadFromYAML は yamlDoc を config.yaml として一時ディレクトリに書き出し、
// 実際の設定ファイル読み込み経路（loadInternal）を通して読み込む。
// 構造体リテラルや JSON 環境変数からの組み立ては viper のキー処理を通らないため、
// 設定ファイル経由でしか起きない変質を検出できない。
func loadFromYAML(t *testing.T, yamlDoc string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlDoc), 0o600))
	t.Chdir(dir)
	return loadInternal(t.Context(), "")
}

const downstreamCIMDClientID = "https://client-a.example.com/oauth-client.json"

// DCR が発行する client_id 相当の、大文字小文字が混在する文字列。
const downstreamDCRClientID = "x1Xe6XPajiLzj7cjYAe6ja9LbzzrwC9J"

const downstreamMixedCasePathClientID = "https://client-b.example.com/OAuth/ClientMetadata.json"

// viper は設定ファイルの map キーを小文字化し（insensitiviseMap）、さらに
// expandEnvVars の viper.Set がキーをドットで分割して入れ子の map にしてしまう。
// そのため下流 client_id を map のキーに置くと完全一致では保持できない。
// clients はリストにして client_id を値の位置へ移してあるので、キー処理の
// 影響を受けないことをこのテストで固定する。

func TestLoadInternal_OAuth2Clients_KeysAreNotNormalized(t *testing.T) {
	t.Setenv("CLIENT_A_SECRET", "secret-a")

	cfg, err := loadFromYAML(t, `
gateway:
  encryptKey: `+validEncryptKey+`
sqlite:
  path: ":memory:"
mcpServers:
  mapped:
    description: mapped backend
    spec: https://example.com/openapi.json
    baseURL: https://example.com
    oauth2:
      authURL: https://example.com/oauth/authorize
      tokenURL: https://example.com/oauth/token
      clients:
        - downstreamClientID: "`+downstreamCIMDClientID+`"
          clientID: upstream-a
          clientSecret: ${CLIENT_A_SECRET}
        - downstreamClientID: "`+downstreamDCRClientID+`"
          clientID: upstream-dcr
        - downstreamClientID: "`+downstreamMixedCasePathClientID+`"
          clientID: upstream-b
`)
	require.NoError(t, err)

	oauth2 := cfg.MCPServer["mapped"].OAuth2
	require.NotNil(t, oauth2)
	require.Len(t, oauth2.Clients, 3)

	// ドットを含む HTTPS URL がそのまま引けること
	got, ok := oauth2.UpstreamClient(downstreamCIMDClientID)
	require.True(t, ok, "dotted HTTPS client_id must resolve; got %+v", oauth2.Clients)
	require.Equal(t, "upstream-a", got.ClientID)
	require.Equal(t, "secret-a", got.ClientSecret)

	// 大文字小文字混在の DCR 発行 ID がそのまま引けること
	got, ok = oauth2.UpstreamClient(downstreamDCRClientID)
	require.True(t, ok, "mixed-case client_id must resolve; got %+v", oauth2.Clients)
	require.Equal(t, "upstream-dcr", got.ClientID)

	// パスに大文字を含む URL がそのまま引けること
	got, ok = oauth2.UpstreamClient(downstreamMixedCasePathClientID)
	require.True(t, ok, "mixed-case path must resolve; got %+v", oauth2.Clients)
	require.Equal(t, "upstream-b", got.ClientID)

	// 小文字化された形では引けないこと（完全一致であることの確認）
	_, ok = oauth2.UpstreamClient(strings.ToLower(downstreamDCRClientID))
	require.False(t, ok, "lower-cased client_id must not resolve")
}

// authParams のパラメータ名も同じ経路を通るため、設定ファイル経由で保持されることを確認する。
func TestLoadInternal_OAuth2AuthParams_FromConfigFile(t *testing.T) {
	cfg, err := loadFromYAML(t, `
gateway:
  encryptKey: `+validEncryptKey+`
sqlite:
  path: ":memory:"
mcpServers:
  mapped:
    description: mapped backend
    spec: https://example.com/openapi.json
    baseURL: https://example.com
    oauth2:
      clientID: shared
      clientSecret: shared-secret
      authURL: https://example.com/oauth/authorize
      tokenURL: https://example.com/oauth/token
      authParams:
        prompt: consent
        access_type: offline
`)
	require.NoError(t, err)

	oauth2 := cfg.MCPServer["mapped"].OAuth2
	require.NotNil(t, oauth2)
	require.Equal(
		t,
		map[string]string{"prompt": "consent", "access_type": "offline"},
		oauth2.AuthParams,
	)
}

// --- mcpServers.<name>.oauth2: clients / authParams を JSON 文字列の環境変数から注入 ---

func TestConfigDecodeHook_OAuth2MapsFromJSONEnvVar(t *testing.T) {
	t.Setenv("OAUTH2_CLIENTS_JSON",
		`[{"downstreamClientID":"https://client.example.com/Meta.json",`+
			`"clientID":"up","clientSecret":"sec"}]`)
	t.Setenv("OAUTH2_AUTH_PARAMS_JSON", `{"prompt":"consent"}`)

	const yamlDoc = `
mcpServers:
  api:
    oauth2:
      clients: ${OAUTH2_CLIENTS_JSON}
      authParams: ${OAUTH2_AUTH_PARAMS_JSON}
`
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yamlDoc)))
	require.NoError(t, expandEnvVars(v))

	var conf Config
	require.NoError(t, v.Unmarshal(&conf, viper.DecodeHook(configDecodeHook())))
	oauth2 := conf.MCPServer["api"].OAuth2
	require.NotNil(t, oauth2)
	require.Equal(
		t,
		[]OAuth2Client{{
			DownstreamClientID: "https://client.example.com/Meta.json",
			ClientID:           "up",
			ClientSecret:       "sec",
		}},
		oauth2.Clients,
	)
	require.Equal(t, map[string]string{"prompt": "consent"}, oauth2.AuthParams)
}

// カンマ区切りの環境変数から []string を組み立てる既存の経路が、
// JSON デコードフックをスライスへ広げた後も壊れていないこと。
func TestConfigDecodeHook_CommaSeparatedSliceStillWorks(t *testing.T) {
	t.Setenv("ALLOWED_HOSTS", "a.example.com,b.example.com")

	const yamlDoc = `
fileFetch:
  allowedHosts: ${ALLOWED_HOSTS}
`
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yamlDoc)))
	require.NoError(t, expandEnvVars(v))

	var conf Config
	require.NoError(t, v.Unmarshal(&conf, viper.DecodeHook(configDecodeHook())))
	require.Equal(t, []string{"a.example.com", "b.example.com"}, conf.FileFetch.AllowedHosts)
}

func TestFileFetchConfig_WithDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   FileFetchConfig
		want int64
	}{
		{"zero value gets default", FileFetchConfig{}, DefaultFileFetchMaxSize},
		{"negative gets default", FileFetchConfig{MaxSize: -1}, DefaultFileFetchMaxSize},
		{"explicit value kept", FileFetchConfig{MaxSize: 1024}, 1024},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.WithDefaults()
			require.Equal(t, tt.want, got.MaxSize)
		})
	}
}
