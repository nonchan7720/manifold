package logging

import (
	"io"
	"log/slog"
	"os"
	"time"
)

func NewJSONHandler() slog.Handler {
	return NewJSONHandlerTo(os.Stdout)
}

// NewJSONHandlerTo is NewJSONHandler writing to w instead of stdout, for
// modes where stdout is not free for logs (the stdio MCP transport).
func NewJSONHandlerTo(w io.Writer) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == "time" {
				return slog.String(a.Key, time.Now().Format(time.RFC3339))
			}
			return a
		},
		AddSource: true,
	})
}
