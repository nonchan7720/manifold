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
	// プロジェクトに config.yaml があるので loadInternal は成功するはず
	cfg, err := loadInternal(t.Context())
	require.NoError(t, err)
	require.NotNil(t, cfg)
	// config.yaml に gateway.port: 9999 がある
	require.Equal(t, 9998, cfg.Gateway.Port)
}

// --- fileFetch ---

func TestLoadInternal_FileFetch_Defaults(t *testing.T) {
	// プロジェクトの config.yaml に fileFetch セクションは無いので、既定値が適用される
	cfg, err := loadInternal(t.Context())
	require.NoError(t, err)
	require.Equal(t, DefaultFileFetchMaxSize, cfg.FileFetch.MaxSize)
	require.False(t, cfg.FileFetch.AllowLocal)
	require.Empty(t, cfg.FileFetch.AllowedHosts)
}

func TestLoadInternal_FileFetch_EnvOverride_MaxSize(t *testing.T) {
	// viper の SetDefault + AutomaticEnv により FILEFETCH_MAXSIZE で上書きできる
	t.Setenv("FILEFETCH_MAXSIZE", "1048576")

	cfg, err := loadInternal(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 1048576, cfg.FileFetch.MaxSize)
}

func TestLoadInternal_FileFetch_EnvOverride_AllowLocal(t *testing.T) {
	// viper の SetDefault + AutomaticEnv により FILEFETCH_ALLOWLOCAL で上書きできる
	t.Setenv("FILEFETCH_ALLOWLOCAL", "true")

	cfg, err := loadInternal(t.Context())
	require.NoError(t, err)
	require.True(t, cfg.FileFetch.AllowLocal)
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
