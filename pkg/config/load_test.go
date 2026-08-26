package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
