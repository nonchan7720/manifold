package storage

import "context"

type MediaUploader interface {
	Do(ctx context.Context, data []byte, contentType string) (id string, url string, _ error)
	AccessCheck(ctx context.Context) error
	Enabled() bool
}
