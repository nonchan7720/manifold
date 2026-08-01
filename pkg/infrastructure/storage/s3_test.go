package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

// stubS3API は S3API を満たすテスト用スタブ。各メソッドの戻り値をフィールドで差し替える。
type stubS3API struct {
	uploadOutput *transfermanager.UploadObjectOutput
	uploadErr    error

	presignURL string
	presignErr error

	accessCheckErr error

	getObjectOutput *s3.GetObjectOutput
	getObjectErr    error
}

func (s *stubS3API) UploadObject(ctx context.Context, input *transfermanager.UploadObjectInput) (*transfermanager.UploadObjectOutput, error) {
	if s.uploadErr != nil {
		return nil, s.uploadErr
	}
	return s.uploadOutput, nil
}

func (s *stubS3API) PresignGetObject(ctx context.Context, params *s3.GetObjectInput, expires time.Duration) (string, error) {
	if s.presignErr != nil {
		return "", s.presignErr
	}
	return s.presignURL, nil
}

func (s *stubS3API) AccessCheck(ctx context.Context, bucket string) error {
	return s.accessCheckErr
}

func (s *stubS3API) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if s.getObjectErr != nil {
		return nil, s.getObjectErr
	}
	return s.getObjectOutput, nil
}

func TestS3Service_SaveContent(t *testing.T) {
	t.Run("UploadObjectがエラーを返したとき元エラーがerrors.Isで辿れる", func(t *testing.T) {
		wantErr := errors.New("connection refused")
		api := &stubS3API{uploadErr: wantErr}
		svc := NewS3Uploader(api, "bucket", "prefix")

		_, _, err := svc.SaveContent(context.Background(), []byte("data"), "image/png")

		require.Error(t, err)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("PresignGetObjectがエラーを返したとき元エラーがerrors.Isで辿れる", func(t *testing.T) {
		wantErr := errors.New("invalid credentials")
		api := &stubS3API{
			uploadOutput: &transfermanager.UploadObjectOutput{},
			presignErr:   wantErr,
		}
		svc := NewS3Uploader(api, "bucket", "prefix")

		_, _, err := svc.SaveContent(context.Background(), []byte("data"), "image/png")

		require.Error(t, err)
		require.ErrorIs(t, err, wantErr)
	})
}

func TestS3Service_DownloadContent(t *testing.T) {
	t.Run("GetObjectがエラーを返したとき元エラーがerrors.Isで辿れる", func(t *testing.T) {
		wantErr := errors.New("no such key")
		api := &stubS3API{getObjectErr: wantErr}
		svc := NewS3Uploader(api, "bucket", "prefix")

		_, _, err := svc.DownloadContent(context.Background(), "some-id")

		require.Error(t, err)
		require.ErrorIs(t, err, wantErr)
	})
}
