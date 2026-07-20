package httphandler

import (
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/logging"
)

var (
	//go:embed media_404.html
	mediaNotFoundPage string
)

type MediaHandler struct {
	ContentManager *storage.ContentManagementService
}

func (m *MediaHandler) DownloadContent(w http.ResponseWriter, r *http.Request) {
	ctx := trace.StartSpan(r.Context(), "MediaHandler/DownloadContent")
	var err error
	defer func() { trace.EndSpan(ctx, err) }()
	id := r.PathValue("id")
	if id == "" {
		err = fmt.Errorf("%s", http.StatusText(http.StatusNotFound))
		http.Error(w, mediaNotFoundPage, http.StatusNotFound)
		return
	}
	body, contentType, err := m.ContentManager.DownloadContent(ctx, id)
	if err != nil {
		slog.ErrorContext(ctx,
			"failed to content download",
			logging.WithStackTrace(err)...,
		)
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	defer body.Close()
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if err != nil {
		slog.ErrorContext(ctx,
			"failed to copy content to response",
			logging.WithStackTrace(err)...,
		)
		return
	}
}
