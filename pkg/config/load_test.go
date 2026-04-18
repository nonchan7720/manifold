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
