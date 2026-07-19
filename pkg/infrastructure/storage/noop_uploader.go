package storage

import (
	"context"
	"errors"
)

type noopUploader struct{}

func NewNoopUploader() MediaUploader {
	return &noopUploader{}
}

func (u *noopUploader) Do(ctx context.Context, data []byte, contentType string) (_ string, _ string, rErr error) {
	return "", "", errors.New("Not implement")
}

func (u *noopUploader) AccessCheck(ctx context.Context) error {
	return errors.New("Not implement")
}

func (u *noopUploader) Enabled() bool { return false }
