package storage

import (
	"context"
	"encoding/base64"
)

type MediaUploadService interface {
	Do(ctx context.Context, data []byte, contentType string) (id string, url string, _ error)
	AccessCheck(ctx context.Context) error
	Enabled() bool
}

type MediaUpload struct {
	MediaUploadService
}

func NewMediaUploader(service MediaUploadService) *MediaUpload {
	return &MediaUpload{
		MediaUploadService: service,
	}
}

func (u *MediaUpload) Do(ctx context.Context, data []byte, contentType string) (id string, url string, _ error) {
	data = decodeBase64(data)
	return u.MediaUploadService.Do(ctx, data, contentType)
}

func decodeBase64(v []byte) (raw []byte) {
	dst := make([]byte, base64.URLEncoding.DecodedLen(len(v)))
	n, err := base64.URLEncoding.Decode(dst, v)
	if err == nil {
		return dst[:n]
	}
	return v
}
