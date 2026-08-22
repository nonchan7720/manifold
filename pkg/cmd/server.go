package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/infrastructure/aws"
	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	"github.com/nonchan7720/manifold/pkg/infrastructure/redis"
	"github.com/nonchan7720/manifold/pkg/infrastructure/sqlite"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	httphandler "github.com/nonchan7720/manifold/pkg/interfaces/http"
	"github.com/nonchan7720/manifold/pkg/interfaces/http/middleware"
	"github.com/nonchan7720/manifold/pkg/internal/logging"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/nonchan7720/manifold/pkg/internal/telemetry"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
	"github.com/nonchan7720/manifold/pkg/services/identity"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
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

// storageHostURL は設定されたホスト URL を解析する。空文字や不正な値は起動を止めず、
// 警告ログを出した上で nil(ストレージ提供の URL をそのまま使う)を返す。
func storageHostURL(ctx context.Context, rawURL, path string) *url.URL {
	if rawURL == "" {
		return nil
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		slog.WarnContext(ctx, "invalid storage host URL; using storage-provided URL",
			slog.String("host_url", rawURL),
			slog.String("error", err.Error()),
		)
		return nil
	}
	return parsedURL.JoinPath(path)
}

// newHTTPHandler composes the shared middleware chain around mux.
func newHTTPHandler(mux http.Handler) http.Handler {
	return middleware.Logging(middleware.Recover(middleware.CorsMiddleware(mux)))
}

// resolveMCPServer resolves the *mcp.Server to serve pathValue: a reverse
// server (per-identityKey server from the edge registry), an MCP backend
// (lazy-connect), or an OpenAPI-backed server. Returns nil (causing the
// caller to answer as "not found") when pathValue is empty, unknown, or its
// backend/reverse connection cannot be established.
func resolveMCPServer(
	ctx context.Context,
	mcpSrv *mcpsrv.MCPServer,
	reverseGateway *mcpsrv.ReverseGateway,
	pathValue string,
) *mcp.Server {
	if pathValue == "" {
		return nil
	}

	if reverseGateway != nil && reverseGateway.HasServer(pathValue) {
		srv, err := reverseGateway.ResolveServer(ctx, pathValue)
		if err != nil {
			slog.ErrorContext(ctx, "failed to resolve reverse mcp server",
				slog.String("server", pathValue), slog.String("error", err.Error()))
			return nil
		}
		return srv
	}

	// MCP バックエンドの場合は遅延接続を行う
	if bc, ok := mcpSrv.BackendClient(pathValue); ok {
		if err := bc.EnsureConnected(ctx); err != nil {
			slog.ErrorContext(ctx, "failed to connect mcp backend",
				slog.String("backend", pathValue),
				slog.String("error", err.Error()))
			return nil
		}
	}

	srv, err := mcpSrv.Server(pathValue)
	if err != nil {
		slog.ErrorContext(
			ctx,
			fmt.Sprintf("failed to not found mcp server: %s", pathValue),
			slog.String("error", err.Error()),
		)
		return nil
	}
	return srv
}

// identityKeyFromReverseServer derives the identityKey for a request bound
// for srv, a reverse-transport server: the fixed StaticIdentityKey under
// static pairing, or the profile named by srv.Identity's Resolver under
// remote pairing (config validation guarantees identityResolvers[srv.Identity]
// exists whenever this runs, short of a wiring bug — see the default branch
// of writeIdentityError).
func identityKeyFromReverseServer(
	ctx context.Context,
	srv *config.Server,
	edgeCfg config.EdgeConfig,
	identityResolvers map[string]identity.Resolver,
	r *http.Request,
) (domainedge.IdentityKey, error) {
	if edgeCfg.IsStaticPairing() {
		return domainedge.StaticIdentityKey, nil
	}
	resolver, ok := identityResolvers[srv.Identity]
	if !ok {
		return "", fmt.Errorf(
			"mcpAuthMiddleware: no identity resolver wired for profile %q", srv.Identity,
		)
	}
	return resolver.Resolve(ctx, r)
}

// writeIdentityError maps identityKeyFromReverseServer's error to the
// JSON error body {"error": code} used across the edge endpoints (see
// httphandler.EdgePairHandler): 401 for a bad/missing credential, 503 while
// the identity source is unreachable, 500 for anything else (a wiring bug,
// since config validation should make every other case unreachable).
func writeIdentityError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, identity.ErrUnavailable):
		status, code = http.StatusServiceUnavailable, "identity_unavailable"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// mcpAuthMiddleware protects the /mcp/{server_name} route. For a
// reverse-transport server it derives identityKey (skipping the pass-through
// JWT check entirely, which has no backend to forward the Bearer token to —
// docs/design/webmcp-reverse-gateway.ja.md「拡張と identity の紐づけ」) and
// stores it in the request context (domainedge.WithIdentityKey) for
// ReverseGateway.ResolveServer to read back. Every other server keeps the
// existing pass-through middleware.JWT check.
func mcpAuthMiddleware(
	servers config.Servers,
	reverseGateway *mcpsrv.ReverseGateway,
	edgeCfg config.EdgeConfig,
	identityResolvers map[string]identity.Resolver,
	pathValueName string,
) func(http.Handler) http.Handler {
	jwt := middleware.JWT(servers, pathValueName)
	return func(next http.Handler) http.Handler {
		withJWT := jwt(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue(pathValueName)
			srv, exists := servers[name]
			if reverseGateway == nil || !exists || !reverseGateway.HasServer(name) {
				withJWT.ServeHTTP(w, r)
				return
			}

			identityKey, err := identityKeyFromReverseServer(
				r.Context(), srv, edgeCfg, identityResolvers, r,
			)
			if err != nil {
				writeIdentityError(w, err)
				return
			}
			ctx := domainedge.WithIdentityKey(r.Context(), identityKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func runGatewayServer(ctx context.Context) error {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	oastomcptool.SetFileFetchConfig(oastomcptool.FileFetchConfig{
		AllowLocal:   globalConfig.FileFetch.AllowLocal,
		AllowedHosts: globalConfig.FileFetch.AllowedHosts,
		MaxSize:      globalConfig.FileFetch.MaxSize,
	})

	storeClient, err := newStoreClient(ctx)
	if err != nil {
		return err
	}
	defer storeClient.Close()

	var (
		mediaService           storage.MediaService
		enabledDownloadContent bool
	)
	switch globalConfig.Storage.Type {
	case "s3":
		awsCfg, err := aws.NewConfig(ctx)
		if err != nil {
			return err
		}
		s3Client := aws.NewS3Client(awsCfg)
		mediaService = storage.NewS3Uploader(
			s3Client,
			globalConfig.Storage.S3.Bucket,
			globalConfig.Storage.S3.KeyPrefix,
		)
		if err := mediaService.AccessCheck(ctx); err != nil {
			return err
		}
		enabledDownloadContent = true
	default:
		mediaService = storage.NewNoopUploader()
	}
	const mediaDownloadPath = "/media/download"
	hostURL := storageHostURL(ctx, globalConfig.Storage.HostURL, mediaDownloadPath)
	contentManagementService := storage.NewContentManagementService(hostURL, mediaService)
	_, cleanup, err := telemetry.NewTracerProvider(ctx, &globalConfig.Telemetry)
	if err != nil {
		return err
	}
	defer cleanup()

	_, metricsHandler, metricsCleanup, err := telemetry.NewMeterProvider(
		ctx,
		&globalConfig.Telemetry,
	)
	if err != nil {
		return err
	}
	defer metricsCleanup()

	_, logsCleanup, err := telemetry.NewLoggerProvider(ctx, &globalConfig.Telemetry)
	if err != nil {
		return err
	}
	defer logsCleanup()

	authHandler := httphandler.NewAuthHandler(
		storeClient,
		globalConfig.MCPServer,
		httphandler.WithEncryptKeyByBase64(globalConfig.Gateway.EncryptKey),
	)
	mcpHandler := httphandler.NewMCPHandler(globalConfig.MCPServer)
	healthHandler := httphandler.NewHealthHandler()
	const pathServerName = "server_name"
	mcpSrv := mcpsrv.NewMCPServer(globalConfig.MCPServer, contentManagementService)
	if err := mcpSrv.Init(ctx); err != nil {
		return err
	}
	defer mcpSrv.Close()

	edgeCfg := globalConfig.Gateway.Edge.WithDefaults()
	pairingService := edgeservices.NewPairingService(storeClient)
	edgeRegistry := edgeservices.NewInMemoryRegistry()
	reverseGateway := mcpsrv.NewReverseGateway(
		edgeRegistry,
		pairingService,
		edgeCfg,
		globalConfig.MCPServer,
	)
	reverseGateway.Init(ctx)
	edgeWSHandler := httphandler.NewEdgeWSHandler(edgeCfg, pairingService, reverseGateway)
	edgePairHandler := httphandler.NewEdgePairHandler(pairingService, edgeCfg)

	// identityResolvers backs mcpAuthMiddleware's remote-pairing branch (see
	// docs/design/webmcp-reverse-gateway.ja.md「ユーザー識別」). A profile
	// failing to construct here (e.g. an unreachable JWKS URL for source:
	// jwt) is a startup error, not a per-request one.
	encryptKey, err := base64.StdEncoding.DecodeString(globalConfig.Gateway.EncryptKey)
	if err != nil {
		return fmt.Errorf("decode gateway.encryptKey: %w", err)
	}
	identityResolvers, err := identity.NewResolvers(ctx, globalConfig.Identities, encryptKey)
	if err != nil {
		return err
	}

	mcpHTTPSrv := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return resolveMCPServer(r.Context(), mcpSrv, reverseGateway, r.PathValue(pathServerName))
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
	mux := http.NewServeMux()
	authHandler.RegisterRoutes(
		mux,
		pathServerName,
		middleware.MCPServerApp(globalConfig.MCPServer, pathServerName),
	)
	mux.Handle(
		fmt.Sprintf("/mcp/{%s}", pathServerName),
		mcpAuthMiddleware(
			globalConfig.MCPServer, reverseGateway, edgeCfg, identityResolvers, pathServerName,
		)(mcpHTTPSrv),
	)
	mux.Handle("/mcp/list", http.HandlerFunc(mcpHandler.MCPList))
	mux.Handle("/healthz", http.HandlerFunc(healthHandler.Healthz))
	mux.Handle("GET /edge/ws", edgeWSHandler)
	mux.HandleFunc("POST /edge/pair", edgePairHandler.Pair)
	if metricsHandler != nil {
		mux.Handle("/metrics", metricsHandler)
	}

	if enabledDownloadContent {
		mediaHandler := &httphandler.MediaHandler{
			ContentManager: contentManagementService,
		}
		mux.Handle(
			fmt.Sprintf("%s/{id}", mediaDownloadPath),
			http.HandlerFunc(mediaHandler.DownloadContent),
		)
	}

	slogHandler := slog.NewMultiHandler(
		logging.NewOTEL(logging.NewJSONHandler()),
		logging.NewOTELLogs(),
	)
	logger := slog.New(slogHandler)
	slog.SetDefault(logger)

	gateway := globalConfig.Gateway
	servePort := gateway.Port
	if servePort == 0 {
		servePort = 8081
	}
	srv := &http.Server{
		Addr: fmt.Sprintf(":%d", servePort),
		Handler: otelhttp.NewHandler(
			newHTTPHandler(mux),
			fmt.Sprintf("%s/%s", trace.OpenTelemetryTracerName, "gateway"),
		),
	}
	return runServer(ctx, srv, "gateway", servePort, gateway.Cert, gateway.Key)
}

// newStoreClient はグローバル設定に基づいてストレージクライアントを生成する。
// sqlite.path が設定されている場合はSQLiteを、memory.enabled が true の場合はインメモリを、
// それ以外の場合はRedisを使用する。
// Redisの設定が無い場合は、外部サービス不要で動作するインメモリにフォールバックする。
func newStoreClient(ctx context.Context) (store.Client, error) {
	if globalConfig.SQLite != nil && globalConfig.SQLite.Path != "" {
		c, err := sqlite.NewClient(ctx, globalConfig.SQLite.Path)
		if err != nil {
			return nil, err
		}
		c.StartCleanup(ctx, 5*time.Minute)
		return c, nil
	}
	if globalConfig.Memory != nil && globalConfig.Memory.Enabled {
		return memory.NewClient(ctx)
	}
	if globalConfig.Redis == nil {
		slog.WarnContext(ctx, "no storage backend configured, falling back to the in-memory store")
		return memory.NewClient(ctx)
	}
	return redis.NewClient(ctx, globalConfig.Redis)
}

// runServer starts an HTTP server and handles graceful shutdown.
func runServer(
	ctx context.Context,
	srv *http.Server,
	name string,
	port int,
	certFile, keyFile string,
) error {
	errCh := make(chan error, 1)
	go func() {
		slog.InfoContext(ctx, "starting server", slog.String("name", name), slog.Int("port", port))
		if certFile != "" && keyFile != "" {
			if err := srv.ListenAndServeTLS(
				certFile,
				keyFile,
			); err != nil &&
				!errors.Is(err, http.ErrServerClosed) {
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
		slog.InfoContext(ctx, "shutdown signal received", slog.String("server", name))
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("%s graceful shutdown error: %w", name, err)
	}
	slog.InfoContext(shutdownCtx, "graceful shutdown completed", slog.String("server", name))
	return nil
}
