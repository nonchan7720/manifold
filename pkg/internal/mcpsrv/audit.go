package mcpsrv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/nonchan7720/manifold/pkg/services/guardrail"
)

// Audit outcomes recorded in the "outcome" field.
const (
	AuditOutcomeSuccess   = "success"
	AuditOutcomeToolError = "tool_error"
	AuditOutcomeDenied    = "denied"
	AuditOutcomeError     = "error"
)

// 監査ログのガードレール（audit.guardrail）を通した引数の扱いを
// "arguments_guardrail" に記録する値。
const (
	// AuditGuardrailNone はガードレールが変更しなかった（引数をそのまま記録）。
	AuditGuardrailNone = "none"
	// AuditGuardrailMasked はガードレールが書き換えた引数を記録した。
	AuditGuardrailMasked = "masked"
	// AuditGuardrailBlocked はガードレールが拒否したので引数を記録しなかった。
	AuditGuardrailBlocked = "blocked"
	// AuditGuardrailError はガードレールの呼び出しに失敗したので引数を記録しなかった。
	AuditGuardrailError = "error"
	// AuditGuardrailSkipped は書き込み待ちが溢れた（またはシャットダウン中）ので、
	// ガードレールに通さず引数を記録しなかった。
	AuditGuardrailSkipped = "skipped"
)

// AuditLogger writes one JSON line per tools/call to its output (see
// config.AuditConfig). It is independent of the application log, so audit
// records can be shipped and retained separately.
//
// With a guardrail (audit.guardrail), records carrying arguments are queued
// and written by background workers after the guardrail has masked the
// arguments, so the guardrail adds no latency to the tool call. The
// arguments are never written unmasked: when the guardrail fails, blocks,
// or the queue is full, the record is written without them.
type AuditLogger struct {
	logger           *slog.Logger
	headers          config.AuthzHeaders
	includeArguments bool
	closer           io.Closer
	now              func() time.Time

	guardrail guardrail.Guardrail
	// mu guards closed and sends on queue against Close.
	mu      sync.RWMutex
	closed  bool
	queue   chan auditEntry
	workers sync.WaitGroup
}

// auditEntry is a record waiting for the guardrail to mask its arguments.
type auditEntry struct {
	ctx       context.Context //nolint:containedctx // 書き込み時のトレース・ログの文脈
	at        time.Time
	attrs     []slog.Attr
	arguments json.RawMessage
	params    map[string]any
}

// NewAuditLogger opens cfg's output and returns the logger. Callers check
// cfg.Enabled first. headers names the request headers carrying the
// caller's user ID and groups (authz.headers, whether or not authz is on).
func NewAuditLogger(cfg config.AuditConfig, headers config.AuthzHeaders) (*AuditLogger, error) {
	var (
		w      io.Writer
		closer io.Closer
	)
	switch output := cfg.OutputOrDefault(); output {
	case config.AuditOutputStdout:
		w = os.Stdout
	case config.AuditOutputStderr:
		w = os.Stderr
	default:
		f, err := os.OpenFile(output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint: gosec
		if err != nil {
			return nil, fmt.Errorf("open audit output %q: %w", output, err)
		}
		w, closer = f, f
	}
	l := newAuditLoggerTo(w, headers, cfg.IncludeArguments)
	l.closer = closer
	if g := cfg.Guardrail; g != nil {
		l.startGuardrail(
			guardrail.NewHTTPGuardrail(*g, nil), g.QueueSizeOrDefault(), g.ConcurrencyOrDefault(),
		)
	}
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

// startGuardrail makes records with arguments go through g on concurrency
// background workers, with at most queueSize records waiting.
func (l *AuditLogger) startGuardrail(g guardrail.Guardrail, queueSize, concurrency int) {
	l.guardrail = g
	l.queue = make(chan auditEntry, queueSize)
	for range concurrency {
		l.workers.Go(func() {
			for e := range l.queue {
				l.writeGuarded(e)
			}
		})
	}
}

// Close writes the records still waiting for the guardrail, then closes the
// output file, if Output named one. Records arriving after Close are written
// without their arguments.
func (l *AuditLogger) Close() error {
	if l == nil {
		return nil
	}
	if l.queue != nil {
		l.mu.Lock()
		if !l.closed {
			l.closed = true
			close(l.queue)
		}
		l.mu.Unlock()
		l.workers.Wait()
	}
	if l.closer == nil {
		return nil
	}
	return l.closer.Close()
}

// write writes one audit line timestamped at, the time the call ended (a
// queued record is written later than that).
func (l *AuditLogger) write(ctx context.Context, at time.Time, attrs []slog.Attr) {
	r := slog.NewRecord(at, slog.LevelInfo, "audit", 0)
	r.AddAttrs(attrs...)
	_ = l.logger.Handler().Handle(ctx, r)
}

// enqueue hands e to the guardrail workers, reporting false when the logger
// is closing or the queue is full.
func (l *AuditLogger) enqueue(e auditEntry) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		return false
	}
	select {
	case l.queue <- e:
		return true
	default:
		return false
	}
}

// writeGuarded passes e's arguments through the guardrail and writes the
// record with the masked arguments, or without them when the guardrail
// blocks or fails.
func (l *AuditLogger) writeGuarded(e auditEntry) {
	res, err := l.guardrail.Apply(e.ctx, guardrail.Request{
		Texts:     []string{string(e.arguments)},
		InputType: guardrail.InputTypeRequest,
		Params:    e.params,
	})
	attrs := e.attrs
	switch {
	case err != nil:
		slog.WarnContext(e.ctx, "audit guardrail failed; recording the call without arguments",
			slog.Any("error", err))
		attrs = append(attrs, slog.String("arguments_guardrail", AuditGuardrailError))
	case res.Action == guardrail.ActionNone:
		attrs = append(attrs,
			slog.Any("arguments", e.arguments),
			slog.String("arguments_guardrail", AuditGuardrailNone))
	case res.Action == guardrail.ActionIntervened:
		attrs = append(attrs,
			maskedArguments(res.Texts[0]),
			slog.String("arguments_guardrail", AuditGuardrailMasked))
	default: // guardrail.ActionBlocked
		attrs = append(attrs, slog.String("arguments_guardrail", AuditGuardrailBlocked))
		if res.BlockedReason != "" {
			attrs = append(attrs, slog.String("arguments_guardrail_reason", res.BlockedReason))
		}
	}
	l.write(e.ctx, e.at, attrs)
}

// maskedArguments records the guardrail's rewrite of the arguments: as JSON
// when it still is (the usual case, e.g. "a@example.com" → "[EMAIL]" inside
// a string), as a string otherwise (e.g. a masked number left unquoted).
func maskedArguments(text string) slog.Attr {
	if json.Valid([]byte(text)) {
		return slog.Any("arguments", json.RawMessage(text))
	}
	return slog.String("arguments", text)
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
	// 呼び出し元がキャンセルしても監査記録は残す。
	ctx = context.WithoutCancel(ctx)
	at := l.now()
	if l.includeArguments && len(params.Arguments) > 0 {
		if l.guardrail == nil {
			attrs = append(attrs, slog.Any("arguments", params.Arguments))
		} else {
			queued := l.enqueue(auditEntry{
				ctx:       ctx,
				at:        at,
				attrs:     attrs,
				arguments: params.Arguments,
				params: map[string]any{
					"source": "manifold-audit", "server": server, "service": service,
					"tool": params.Name,
				},
			})
			if queued {
				return
			}
			attrs = append(attrs, slog.String("arguments_guardrail", AuditGuardrailSkipped))
		}
	}
	l.write(ctx, at, attrs)
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
