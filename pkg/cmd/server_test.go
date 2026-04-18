package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
