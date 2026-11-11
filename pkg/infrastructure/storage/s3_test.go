package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
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

	// 直近の呼び出しで渡されたオブジェクトキー。
	uploadKey  string
	presignKey string
	getKey     string
}

func (s *stubS3API) UploadObject(
	ctx context.Context,
	input *transfermanager.UploadObjectInput,
) (*transfermanager.UploadObjectOutput, error) {
	s.uploadKey = aws.ToString(input.Key)
	if s.uploadErr != nil {
		return nil, s.uploadErr
	}
	return s.uploadOutput, nil
}

func (s *stubS3API) PresignGetObject(
	ctx context.Context,
	params *s3.GetObjectInput,
	expires time.Duration,
) (string, error) {
	s.presignKey = aws.ToString(params.Key)
	if s.presignErr != nil {
		return "", s.presignErr
	}
	return s.presignURL, nil
}

func (s *stubS3API) AccessCheck(ctx context.Context, bucket string) error {
	return s.accessCheckErr
}

func (s *stubS3API) GetObject(
	ctx context.Context,
	params *s3.GetObjectInput,
	optFns ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	s.getKey = aws.ToString(params.Key)
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

func TestS3Service_SaveContent_AppendsExtensionToID(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		data        []byte
		wantExt     string
	}{
		{
			name:        "Content-Type から拡張子を引く",
			contentType: "application/pdf",
			data:        pdfHeader,
			wantExt:     ".pdf",
		},
		{
			name:        "パラメータ付きの Content-Type",
			contentType: "image/png; charset=binary",
			data:        pngHeader,
			wantExt:     ".png",
		},
		{
			name:        "octet-stream は実体から判定する",
			contentType: "application/octet-stream",
			data:        pngHeader,
			wantExt:     ".png",
		},
		{name: "Content-Type 未指定は実体から判定する", contentType: "", data: jpegHeader, wantExt: ".jpg"},
		{
			name:        "mimetype の知らない Content-Type は実体から判定する",
			contentType: "application/x-unknown-vendor",
			data:        gifHeader,
			wantExt:     ".gif",
		},
		{
			name:        "docx",
			contentType: "application/octet-stream",
			data:        nil, // 下で差し替える
			wantExt:     ".docx",
		},
		{
			name:        "判定できなければ拡張子を付けない",
			contentType: "application/octet-stream",
			data:        []byte{0x00, 0x01, 0x02, 0x03},
			wantExt:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.data
			if tt.wantExt == ".docx" {
				data = docxBytes(t, 0)
			}
			api := &stubS3API{presignURL: "https://example.com/presigned"}
			svc := NewS3Uploader(api, "bucket", "prefix")

			id, _, err := svc.SaveContent(context.Background(), data, tt.contentType)
			require.NoError(t, err)

			base := strings.TrimSuffix(id, tt.wantExt)
			require.NoError(t, uuid.Validate(base), "id=%s", id)
			require.Equal(t, base+tt.wantExt, id)
			// アップロード先と署名付き URL のキーは、返した ID と一致する。
			require.Equal(t, "prefix/"+id, api.uploadKey)
			require.Equal(t, "prefix/"+id, api.presignKey)
		})
	}
}

func TestS3Service_DownloadContent_UsesIDAsKey(t *testing.T) {
	// ID はそのままキーになる。拡張子付きの新しい ID も、拡張子の無い旧 ID も読める。
	for _, id := range []string{
		"019a0000-0000-7000-8000-000000000000.pdf",
		"019a0000-0000-7000-8000-000000000000",
	} {
		api := &stubS3API{getObjectOutput: &s3.GetObjectOutput{
			Body:        io.NopCloser(strings.NewReader("x")),
			ContentType: aws.String("application/pdf"),
		}}
		svc := NewS3Uploader(api, "bucket", "prefix")

		body, contentType, err := svc.DownloadContent(context.Background(), id)
		require.NoError(t, err)
		require.NoError(t, body.Close())
		require.Equal(t, "application/pdf", contentType)
		require.Equal(t, "prefix/"+id, api.getKey)
	}
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
