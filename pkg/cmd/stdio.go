package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/logging"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/spf13/cobra"
)

func newStdioCmd() *cobra.Command {
	var serverName string
	cmd := &cobra.Command{
		Use:   "stdio",
		Short: "Serve MCP over stdin/stdout for local MCP clients",
		Long: `Serve MCP over stdin/stdout, so a local MCP client (Claude Desktop, Cursor,
Claude Code, ...) can launch Manifold as a command.

With --server, that server is served. Without it, a config with a single
server serves that server, and otherwise every server that can run over stdio
is served together, each tool named <server>__<tool> (see gateway.aggregate).

Servers that need the caller's own credentials (oauth2, tokenExchange) or a
browser (reverse) can't be served over stdio, and authz must be disabled:
there are no HTTP headers to identify the caller.`,
		Example: `  # Serve an OpenAPI spec without a config file
  manifold stdio --openapi https://petstore3.swagger.io/api/v3/openapi.json

  # Serve one server from config.yaml
  manifold stdio -c config --server petstore`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()
			return runStdio(ctx, globalConfig, serverName, &mcp.StdioTransport{})
		},
	}
	cmd.Flags().StringVar(&serverName, "server", "", "serve only this server")
	quickStart.register(cmd.Flags(), false)
	return cmd
}

// stdioIncompatibility reports why srv can't be served over stdio, or "" when
// it can.
func stdioIncompatibility(srv *config.Server) string {
	switch {
	case srv.IsReverseBackend():
		return "reverse transport servers need a browser connection"
	case srv.OAuth2 != nil:
		return "oauth2 servers need the caller's own OAuth token"
	case srv.TokenExchange != nil:
		return "tokenExchange servers need the caller's own token"
	default:
		return ""
	}
}

// stdioTarget is what runStdio serves: a single server, or the members of
// an aggregated server.
type stdioTarget struct {
	single  string
	members []string
}

// resolveStdioTarget picks the servers stdio mode serves (see newStdioCmd).
func resolveStdioTarget(
	ctx context.Context,
	cfg *config.Config,
	serverName string,
) (stdioTarget, error) {
	if serverName != "" {
		srv, ok := cfg.MCPServer[serverName]
		if !ok {
			return stdioTarget{}, fmt.Errorf("server %q is not defined", serverName)
		}
		if reason := stdioIncompatibility(srv); reason != "" {
			return stdioTarget{}, fmt.Errorf(
				"server %q can't be served over stdio: %s",
				serverName,
				reason,
			)
		}
		return stdioTarget{single: serverName}, nil
	}
	if len(cfg.MCPServer) == 1 {
		for name := range cfg.MCPServer {
			return resolveStdioTarget(ctx, cfg, name)
		}
	}
	explicit := len(cfg.Gateway.Aggregate.Servers) > 0
	var members []string
	for _, name := range cfg.Gateway.Aggregate.Members(cfg.MCPServer) {
		reason := stdioIncompatibility(cfg.MCPServer[name])
		switch {
		case reason == "":
			members = append(members, name)
		case explicit:
			return stdioTarget{}, fmt.Errorf(
				"gateway.aggregate.servers: %q can't be served over stdio: %s", name, reason,
			)
		default:
			slog.WarnContext(ctx, "server skipped in stdio mode",
				slog.String("server", name), slog.String("reason", reason))
		}
	}
	if len(members) == 0 {
		return stdioTarget{}, fmt.Errorf("no server can be served over stdio")
	}
	// gateway.aggregate を有効にしていない設定は読み込み時にサーバー名と
	// 区切り文字の衝突を検証していないため、ここで確認する。
	agg := cfg.Gateway.Aggregate
	agg.Enabled, agg.Servers = true, members
	if err := agg.ValidateServers(ctx, cfg.MCPServer); err != nil {
		return stdioTarget{}, err
	}
	return stdioTarget{members: members}, nil
}

// runStdio serves cfg's servers (picked by resolveStdioTarget) over
// transport until ctx is done or the client disconnects. stdout belongs to
// the MCP transport, so logs go to stderr.
func runStdio(
	ctx context.Context,
	cfg *config.Config,
	serverName string,
	transport mcp.Transport,
) error {
	slog.SetDefault(slog.New(slog.NewMultiHandler(
		logging.NewOTEL(logging.NewJSONHandlerTo(os.Stderr)),
		logging.NewOTELLogs(),
	)))

	if cfg.Authz.Enabled {
		return fmt.Errorf(
			"authz is not supported in stdio mode: there are no HTTP headers to identify the caller",
		)
	}
	if cfg.Audit.Enabled && cfg.Audit.OutputOrDefault() == config.AuditOutputStdout {
		return fmt.Errorf(
			"audit.output can't be stdout in stdio mode (stdout carries MCP messages)",
		)
	}
	target, err := resolveStdioTarget(ctx, cfg, serverName)
	if err != nil {
		return err
	}

	oastomcptool.SetFileFetchConfig(oastomcptool.FileFetchConfig{
		AllowLocal:   cfg.FileFetch.AllowLocal,
		AllowedHosts: cfg.FileFetch.AllowedHosts,
		MaxSize:      cfg.FileFetch.MaxSize,
	})
	prevConfig := globalConfig
	globalConfig = cfg
	defer func() { globalConfig = prevConfig }()
	_, telemetryCleanup, err := setupTelemetry(ctx)
	if err != nil {
		return err
	}
	defer telemetryCleanup()
	toolCache, auditLogger, err := newToolCacheAndAudit()
	if err != nil {
		return err
	}
	defer func() { _ = auditLogger.Close() }()

	// stdio では /media/download を提供できないため、バイナリは resource link に
	// せずレスポンスへそのまま埋め込む。
	selected := config.Servers{}
	for _, name := range append(slices.Clone(target.members), target.single) {
		if name != "" {
			selected[name] = cfg.MCPServer[name]
		}
	}
	mcpSrv, err := newMCPServer(
		ctx,
		selected,
		storage.NewContentManagementService(nil, storage.NewNoopUploader()),
		cfg.Gateway,
		nil,
		mcpsrv.WithToolCache(toolCache),
		mcpsrv.WithAuditLogger(auditLogger),
	)
	if err != nil {
		return err
	}
	defer mcpSrv.Close()

	var srv *mcp.Server
	if target.single != "" {
		srv, err = mcpSrv.Server(target.single)
	} else {
		srv, err = mcpSrv.NewAggregateServer(
			ctx, target.members,
			cfg.Gateway.Aggregate.SeparatorOrDefault(), cfg.Gateway.Aggregate.ToolSearch,
		)
	}
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "serving mcp over stdio",
		slog.Any("servers", slices.Sorted(maps.Keys(selected))))
	return srv.Run(ctx, transport)
}
