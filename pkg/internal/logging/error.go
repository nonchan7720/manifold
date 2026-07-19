package logging

import (
	"log/slog"
	"runtime/debug"
)

func WithStackTrace(err error) []any {
	stackTrace := string(debug.Stack())
	return []any{
		slog.Any("error", err),
		slog.String("stack_trace", stackTrace),
	}
}
