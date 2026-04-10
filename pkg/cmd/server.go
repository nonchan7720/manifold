package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/infrastructure/redis"
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
	redisClient, err := redis.NewClient(ctx, globalConfig.Redis)
	if err != nil {
		return err
	}

	authHandler := httphandler.NewAuthHandler(redisClient, globalConfig.MCPServer)

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
