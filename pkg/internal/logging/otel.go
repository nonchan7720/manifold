package logging

import (
	"context"
	"log/slog"
	"os"
	"strconv"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/version"
	"go.opentelemetry.io/contrib/bridges/otelslog"
)

func NewOTELLogs() slog.Handler {
	return otelslog.NewHandler("manifold")
}

type otelHandler struct {
	slog.Handler
}

func NewOTEL(h slog.Handler) slog.Handler {
	return &otelHandler{Handler: h}
}

func (h *otelHandler) Handle(ctx context.Context, record slog.Record) error {
	span := trace.SpanFromContext(ctx)
	if span != nil && span.SpanContext().IsValid() {
		spanCtx := span.SpanContext()
		attrs := []slog.Attr{
			slog.String("trace_id", spanCtx.TraceID().String()),
			slog.String("span_id", spanCtx.SpanID().String()),
			slog.String("trace.id", spanCtx.TraceID().String()),
			slog.String("span.id", spanCtx.SpanID().String()),
		}
		var datadogFlg bool
		if v := os.Getenv("DD_SERVICE"); v != "" {
			datadogFlg = true
			attrs = append(attrs, slog.String("dd.service", v))
		}
		if v := os.Getenv("DD_ENV"); v != "" {
			datadogFlg = true
			attrs = append(attrs, slog.String("dd.env", v))
		}
		if datadogFlg {
			attrs = append(attrs,
				slog.String("dd.trace_id", convertTraceID(spanCtx.TraceID().String())),
				slog.String("dd.span_id", convertTraceID(spanCtx.SpanID().String())),
				slog.String("dd.version", version.MarkVersion),
			)
		}
		record.AddAttrs(attrs...)
	}
	return h.Handler.Handle(ctx, record)
}

func (h *otelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return NewOTEL(h.Handler.WithAttrs(attrs))
}

func (h *otelHandler) WithGroup(name string) slog.Handler {
	return NewOTEL(h.Handler.WithGroup(name))
}

func convertTraceID(id string) string {
	if len(id) < 16 {
		return ""
	}
	if len(id) > 16 {
		id = id[16:]
	}
	intValue, err := strconv.ParseUint(id, 16, 64)
	if err != nil {
		return ""
	}
	return strconv.FormatUint(intValue, 10)
}
