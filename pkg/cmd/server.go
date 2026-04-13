package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/infrastructure/redis"
	"github.com/nonchan7720/manifold/pkg/infrastructure/sqlite"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	httphandler "github.com/nonchan7720/manifold/pkg/interfaces/http"
	"github.com/nonchan7720/manifold/pkg/interfaces/http/middleware"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	"github.com/spf13/cobra"
)

func newGatewayCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gateway",
		Short: "Start mcp gateway server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGatewayServer(cmd.Context())
		},
	}
}

func runGatewayServer(ctx context.Context) error {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	storeClient, err := newStoreClient(ctx)
	if err != nil {
		return err
	}
	defer storeClient.Close()

	authHandler := httphandler.NewAuthHandler(storeClient, globalConfig.MCPServer, httphandler.WithEncryptKeyByBase64(globalConfig.Gateway.EncryptKey))
	mcpHandler := httphandler.NewMCPHandler(globalConfig.MCPServer)
	const pathServerName = "server_name"
	mcpSrv := mcpsrv.NewMCPServer(globalConfig.MCPServer)
	if err := mcpSrv.Init(ctx); err != nil {
		return err
	}
	defer mcpSrv.Close()

	mcpHTTPSrv := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		pathValue := r.PathValue(pathServerName)
		if pathValue == "" {
			return nil
		}

		// MCP バックエンドの場合は遅延接続を行う
		if bc, ok := mcpSrv.BackendClient(pathValue); ok {
			if err := bc.EnsureConnected(r.Context()); err != nil {
				slog.ErrorContext(r.Context(), "failed to connect mcp backend",
					slog.String("backend", pathValue),
					slog.String("error", err.Error()))
				return nil
			}
		}

		if srv, err := mcpSrv.Server(pathValue); err != nil {
			slog.ErrorContext(r.Context(), fmt.Sprintf("failed to not found mcp server: %s", pathValue), slog.String("error", err.Error()))
			return nil
		} else {
			return srv
		}
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
	mux := http.NewServeMux()
	authHandler.RegisterRoutes(mux, pathServerName, middleware.MCPServerApp(globalConfig.MCPServer, pathServerName))
	mux.Handle(fmt.Sprintf("/mcp/{%s}", pathServerName), middleware.JWT(globalConfig.MCPServer, pathServerName)(mcpHTTPSrv))
	mux.Handle("/mcp/list", http.HandlerFunc(mcpHandler.MCPList))

	gateway := globalConfig.Gateway
	servePort := gateway.Port
	if servePort == 0 {
		servePort = 8081
	}
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", servePort),
		Handler: middleware.Logging(middleware.Recover(middleware.CorsMiddleware(mux))),
	}
	return runServer(ctx, srv, "gateway", servePort, gateway.Cert, gateway.Key)
}

// newStoreClient はグローバル設定に基づいてストレージクライアントを生成する。
// sqlite.path が設定されている場合はSQLiteを使用し、それ以外はRedisを使用する。
func newStoreClient(ctx context.Context) (store.Client, error) {
	if globalConfig.SQLite.Path != "" {
		c, err := sqlite.NewClient(ctx, globalConfig.SQLite.Path)
		if err != nil {
			return nil, err
		}
		c.StartCleanup(ctx, 5*time.Minute)
		return c, nil
	}
	return redis.NewClient(ctx, globalConfig.Redis)
}

// runServer starts an HTTP server and handles graceful shutdown.
func runServer(ctx context.Context, srv *http.Server, name string, port int, certFile, keyFile string) error {
	errCh := make(chan error, 1)
	go func() {
		slog.Info("starting server", slog.String("name", name), slog.Int("port", port))
		if certFile != "" && keyFile != "" {
			if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s error: %w", name, err)
			}
		} else {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s error: %w", name, err)
			}
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received", slog.String("server", name))
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("%s graceful shutdown error: %w", name, err)
	}
	slog.Info("graceful shutdown completed", slog.String("server", name))
	return nil
}
