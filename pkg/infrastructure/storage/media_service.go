package storage

import (
	"context"
	"encoding/base64"
	"io"
	"net/url"
)

type MediaService interface {
	SaveContent(
		ctx context.Context,
		data []byte,
		contentType string,
	) (id string, url string, _ error)
	DownloadContent(ctx context.Context, id string) (io.ReadCloser, string, error)
	AccessCheck(ctx context.Context) error
	Enabled() bool
}

type ContentManagementService struct {
	MediaService

	hostURL *url.URL
}

func NewContentManagementService(hostURL *url.URL, service MediaService) *ContentManagementService {
	return &ContentManagementService{
		MediaService: service,
		hostURL:      hostURL,
	}
}

func (u *ContentManagementService) SaveContent(
	ctx context.Context,
	data []byte,
	contentType string,
) (id string, url string, _ error) {
	data = decodeBase64(data)
	id, url, err := u.MediaService.SaveContent(ctx, data, contentType)
	if err != nil {
		return "", "", err
	}
	if u.hostURL == nil {
		return id, url, nil
	}
	respURL := u.hostURL.JoinPath(id)
	return id, respURL.String(), nil
}

func decodeBase64(v []byte) (raw []byte) {
	dst := make([]byte, base64.URLEncoding.DecodedLen(len(v)))
	n, err := base64.URLEncoding.Decode(dst, v)
	if err == nil {
		return dst[:n]
	}
	return v
}
