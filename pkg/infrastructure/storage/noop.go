package storage

import (
	"context"
	"errors"
	"io"
)

var (
	ErrNotImplement = errors.New("not implement")
)

type noopUploader struct{}

func NewNoopUploader() MediaService {
	return &noopUploader{}
}

func (u *noopUploader) SaveContent(
	ctx context.Context,
	data []byte,
	contentType string,
) (_ string, _ string, rErr error) {
	return "", "", ErrNotImplement
}

func (u *noopUploader) AccessCheck(ctx context.Context) error {
	return ErrNotImplement
}

func (u *noopUploader) Enabled() bool { return false }

func (u *noopUploader) DownloadContent(
	ctx context.Context,
	id string,
) (io.ReadCloser, string, error) {
	return nil, "", ErrNotImplement
}
