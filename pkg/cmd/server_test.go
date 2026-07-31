package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	"github.com/nonchan7720/manifold/pkg/infrastructure/sqlite"
	"github.com/stretchr/testify/require"
)

// withGlobalConfig は globalConfig を一時的に差し替え、テスト終了時に元に戻す。
func withGlobalConfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	prev := globalConfig
	globalConfig = cfg
	t.Cleanup(func() { globalConfig = prev })
}

func TestNewStoreClient_SQLite(t *testing.T) {
	withGlobalConfig(t, &config.Config{
		SQLite: &config.SQLiteConfig{Path: ":memory:"},
	})

	c, err := newStoreClient(t.Context())
	require.NoError(t, err)
	defer c.Close()

	require.IsType(t, &sqlite.Client{}, c)
}

func TestNewStoreClient_Memory(t *testing.T) {
	withGlobalConfig(t, &config.Config{
		Memory: &config.MemoryConfig{Enabled: true},
	})

	c, err := newStoreClient(t.Context())
	require.NoError(t, err)
	defer c.Close()

	require.IsType(t, &memory.Client{}, c)
}

func TestNewStoreClient_SQLiteNil_DoesNotPanic(t *testing.T) {
	// SQLite が nil の場合でもパニックせず、Memory 分岐にフォールバックできること
	withGlobalConfig(t, &config.Config{
		SQLite: nil,
		Memory: &config.MemoryConfig{Enabled: true},
	})

	require.NotPanics(t, func() {
		c, err := newStoreClient(t.Context())
		require.NoError(t, err)
		defer c.Close()
	})
}

func TestNewStoreClient_MemoryDisabled_FallsBackToMemory(t *testing.T) {
	// memory セクションだけが存在し enabled が false でも、Redis 設定が無ければ
	// パニックせずインメモリにフォールバックすること
	withGlobalConfig(t, &config.Config{
		Memory: &config.MemoryConfig{Enabled: false},
	})

	c, err := newStoreClient(t.Context())
	require.NoError(t, err)
	defer c.Close()

	require.IsType(t, &memory.Client{}, c)
}

func TestNewStoreClient_SQLiteEmptyPath_FallsBackToMemory(t *testing.T) {
	// sqlite.path が空文字の場合も Redis 設定が無ければインメモリにフォールバックすること
	withGlobalConfig(t, &config.Config{
		SQLite: &config.SQLiteConfig{Path: ""},
	})

	c, err := newStoreClient(t.Context())
	require.NoError(t, err)
	defer c.Close()

	require.IsType(t, &memory.Client{}, c)
}

func TestNewStoreClient_MemoryDisabled_PrefersRedis(t *testing.T) {
	// memory.enabled が false の場合は Redis 設定が優先されること。
	// 接続先が存在しないため接続エラーになるが、インメモリにフォールバックしないことを確認する。
	withGlobalConfig(t, &config.Config{
		Memory: &config.MemoryConfig{Enabled: false},
		Redis:  &config.RedisConfig{Addrs: []string{"127.0.0.1:1"}},
	})

	c, err := newStoreClient(t.Context())
	require.Error(t, err, "redis へ接続を試みるべき")
	require.ErrorContains(t, err, "redis")
	require.Nil(t, c)
}

func TestNewGatewayCmd(t *testing.T) {
	cmd := newGatewayCmd()
	require.Equal(t, "gateway", cmd.Use)
	require.Equal(t, "Start mcp gateway server", cmd.Short)
	require.NotNil(t, cmd.RunE)
}

func TestRunServer_GracefulShutdown(t *testing.T) {
	// httptest でランダムポートのサーバーを作成
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// テスト用HTTPサーバーをランダムポートで起動
	ts := httptest.NewServer(mux)
	ts.Close() // すぐに閉じる（ポートだけ取得）

	srv := &http.Server{
		Addr:    "127.0.0.1:0",
		Handler: mux,
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServer(ctx, srv, "test-server", 0, "", "")
	}()

	// サーバーが起動するのを少し待ってからキャンセル
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		// グレースフルシャットダウンはエラーなし
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("runServer did not return in time")
	}
}

func TestRunServer_ServerError(t *testing.T) {
	// すでに使用中のポートでサーバーを起動しようとするとエラー
	// まず既存サーバーでポートを使用
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer listener.Close()

	addr := listener.Listener.Addr().String()
	srv := &http.Server{
		Addr:    addr,
		Handler: http.DefaultServeMux,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := runServer(ctx, srv, "test-server", 0, "", "")
	// ポートが使用中のためエラーが返る
	require.Error(t, err)
	require.Contains(t, err.Error(), "test-server error")
}
