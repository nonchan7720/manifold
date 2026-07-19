package storage

import (
	"context"
	"errors"
)

var (
	ErrNotImplement = errors.New("not implement")
)

type noopUploader struct{}

func NewNoopUploader() MediaUploadService {
	return &noopUploader{}
}

func (u *noopUploader) Do(ctx context.Context, data []byte, contentType string) (_ string, _ string, rErr error) {
	return "", "", ErrNotImplement
}

func (u *noopUploader) AccessCheck(ctx context.Context) error {
	return ErrNotImplement
}

func (u *noopUploader) Enabled() bool { return false }
