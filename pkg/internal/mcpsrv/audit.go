package mcpsrv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

// Audit outcomes recorded in the "outcome" field.
const (
	AuditOutcomeSuccess   = "success"
	AuditOutcomeToolError = "tool_error"
	AuditOutcomeDenied    = "denied"
	AuditOutcomeError     = "error"
)

// reopenableFile is an append-only log file that can be reopened after
// logrotate renames or removes it.
type reopenableFile struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

func openAuditFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint: gosec
}

func openReopenableFile(path string) (*reopenableFile, error) {
	f, err := openAuditFile(path)
	if err != nil {
		return nil, err
	}
	return &reopenableFile{path: path, f: f}, nil
}

func (r *reopenableFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Write(p)
}

// Reopen opens path anew and swaps it in. On failure the current file is
// kept, so records are not lost.
func (r *reopenableFile) Reopen() error {
	f, err := openAuditFile(r.path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	old := r.f
	r.f = f
	r.mu.Unlock()
	return old.Close()
}

func (r *reopenableFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}

// AuditLogger writes one JSON line per tools/call to its output (see
// config.AuditConfig). It is independent of the application log, so audit
// records can be shipped and retained separately.
type AuditLogger struct {
	logger           *slog.Logger
	headers          config.AuthzHeaders
	includeArguments bool
	closer           io.Closer
	file             *reopenableFile // non-nil when Output is a file path
	now              func() time.Time
}

// NewAuditLogger opens cfg's output and returns the logger. Callers check
// cfg.Enabled first. headers names the request headers carrying the
// caller's user ID and groups (authz.headers, whether or not authz is on).
func NewAuditLogger(cfg config.AuditConfig, headers config.AuthzHeaders) (*AuditLogger, error) {
	var (
		w      io.Writer
		closer io.Closer
		file   *reopenableFile
	)
	switch output := cfg.OutputOrDefault(); output {
	case config.AuditOutputStdout:
		w = os.Stdout
	case config.AuditOutputStderr:
		w = os.Stderr
	default:
		f, err := openReopenableFile(output)
		if err != nil {
			return nil, fmt.Errorf("open audit output %q: %w", output, err)
		}
		w, closer, file = f, f, f
	}
	l := newAuditLoggerTo(w, headers, cfg.IncludeArguments)
	l.closer = closer
	l.file = file
	return l, nil
}

func newAuditLoggerTo(
	w io.Writer,
	headers config.AuthzHeaders,
	includeArguments bool,
) *AuditLogger {
	return &AuditLogger{
		logger:           slog.New(slog.NewJSONHandler(w, nil)),
		headers:          headers,
		includeArguments: includeArguments,
		now:              time.Now,
	}
}

// Close closes the output file, if Output named one.
func (l *AuditLogger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	return l.closer.Close()
}

// ReopenOnSIGHUP reopens the output file on SIGHUP until ctx is done, so
// logrotate (without copytruncate) can rename it and signal the gateway.
// It does nothing for stdout / stderr, leaving SIGHUP's default behavior.
func (l *AuditLogger) ReopenOnSIGHUP(ctx context.Context) {
	if l == nil || l.file == nil {
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	go func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				if err := l.file.Reopen(); err != nil {
					slog.ErrorContext(ctx, "audit: reopen output on SIGHUP failed",
						slog.Any("error", err))
					continue
				}
				slog.InfoContext(ctx, "audit: reopened output on SIGHUP")
			}
		}
	}()
}

// tokenFingerprint identifies the caller's bearer token without recording
// it: the first 12 hex characters of its SHA-256.
func tokenFingerprint(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

func auditOutcome(res mcp.Result, err error) string {
	switch {
	case errors.Is(err, errToolNotAllowedByPolicy):
		return AuditOutcomeDenied
	case err != nil:
		return AuditOutcomeError
	}
	if callRes, ok := res.(*mcp.CallToolResult); ok && callRes.IsError {
		return AuditOutcomeToolError
	}
	return AuditOutcomeSuccess
}

func (l *AuditLogger) record(
	ctx context.Context,
	server, service string,
	req mcp.Request,
	params *mcp.CallToolParamsRaw,
	start time.Time,
	res mcp.Result,
	err error,
) {
	attrs := []slog.Attr{
		slog.String("event", "tool_call"),
		slog.String("server", server),
		slog.String("service", service),
		slog.String("tool", params.Name),
		slog.String("outcome", auditOutcome(res, err)),
		slog.Int64("duration_ms", l.now().Sub(start).Milliseconds()),
	}
	if extra := req.GetExtra(); extra != nil && extra.Header != nil {
		if user := extra.Header.Get(l.headers.UserID); user != "" {
			attrs = append(attrs, slog.String("user", user))
		}
		if groups := extra.Header.Get(l.headers.UserGroups); groups != "" {
			attrs = append(attrs, slog.String("groups", groups))
		}
	}
	if fp := tokenFingerprint(contexts.FromRequestAuthHeader(ctx)); fp != "" {
		attrs = append(attrs, slog.String("token", fp))
	}
	// 逆方向（WebMCP）のエンドポイントは JWT を検証しないので user / groups /
	// token が空になる。呼び出し元は identityKey でしか分からない。
	if key, ok := domainedge.IdentityKeyFromContext(ctx); ok && key != "" {
		attrs = append(attrs, slog.String("identity", string(key)))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	if l.includeArguments && len(params.Arguments) > 0 {
		attrs = append(attrs, slog.Any("arguments", params.Arguments))
	}
	// 呼び出し元がキャンセルしても監査記録は残す。
	l.logger.LogAttrs(context.WithoutCancel(ctx), slog.LevelInfo, "audit", attrs...)
}

// newAuditMiddleware returns the middleware recording every tools/call on
// server, or nil when l is nil. It sits outside authz so denied calls are
// recorded too.
func newAuditMiddleware(server, service string, l *AuditLogger) mcp.Middleware {
	if l == nil {
		return nil
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != authzMethodToolsCall {
				return next(ctx, method, req)
			}
			params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
			if !ok {
				return next(ctx, method, req)
			}
			start := l.now()
			res, err := next(ctx, method, req)
			l.record(ctx, server, service, req, params, start, res, err)
			return res, err
		}
	}
}
