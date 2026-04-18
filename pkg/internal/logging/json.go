package logging

import (
	"log/slog"
	"os"
	"time"
)

func NewJSONHandler() slog.Handler {
	return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == "time" {
				return slog.String(a.Key, time.Now().Format(time.RFC3339))
			}
			return a
		},
		AddSource: true,
	})
}
