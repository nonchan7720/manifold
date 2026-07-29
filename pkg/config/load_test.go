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

// --- gateway.toolSearch ---

func TestLoadInternal_ToolSearch_Defaults(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	// プロジェクトの config.yaml に gateway.toolSearch セクションは無いので、既定値が適用される
	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, DefaultToolSearchThreshold, cfg.Gateway.ToolSearch.Threshold)
	require.Equal(t, DefaultToolSearchLimit, cfg.Gateway.ToolSearch.DefaultLimit)
	require.Equal(t, ToolSearchResultFormatDefault, cfg.Gateway.ToolSearch.ResultFormat)
	require.Equal(t, DefaultToolSearchDigestMaxTools, cfg.Gateway.ToolSearch.DigestMaxTools)
}

func TestLoadInternal_ToolSearch_EnvOverride_DigestMaxTools(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	t.Setenv("GATEWAY_TOOLSEARCH_DIGESTMAXTOOLS", "20")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, 20, cfg.Gateway.ToolSearch.DigestMaxTools)
}

// TestLoadInternal_ToolSearch_ExplicitZeroDigestMaxTools_ReturnsValidationError は、
// digestMaxTools が明示的に 0 に設定された場合、WithDefaults による -1 への正規化より前に
// validation が実行され、確実にエラーとして検出されることを確認する
// （load.go 内で validation.ValidateWithContext を WithDefaults の defensive fallback より
// 前に実行する順序に依存する）。
func TestLoadInternal_ToolSearch_ExplicitZeroDigestMaxTools_ReturnsValidationError(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	t.Setenv("GATEWAY_TOOLSEARCH_DIGESTMAXTOOLS", "0")

	_, err := loadInternal(t.Context(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be -1 (all) or a positive number")
}

func TestLoadInternal_ToolSearch_EnvOverride_ResultFormat(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	t.Setenv("GATEWAY_TOOLSEARCH_RESULTFORMAT", "claude")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, ToolSearchResultFormatClaude, cfg.Gateway.ToolSearch.ResultFormat)
}

func TestLoadInternal_ToolSearch_EnvOverride_Threshold(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	t.Setenv("GATEWAY_TOOLSEARCH_THRESHOLD", "5")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, 5, cfg.Gateway.ToolSearch.Threshold)
}

func TestLoadInternal_ToolSearch_EnvOverride_DefaultLimit(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "dummy")
	t.Setenv("GOOGLE_CLIENT_SECRET", "dummy")
	t.Setenv("GATEWAY_TOOLSEARCH_DEFAULTLIMIT", "3")

	cfg, err := loadInternal(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, 3, cfg.Gateway.ToolSearch.DefaultLimit)
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
