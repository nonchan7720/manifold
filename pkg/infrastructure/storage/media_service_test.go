package storage

import (
	"context"
	"encoding/base64"
	"io"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeMediaService struct {
	enabled bool
	doFunc  func(ctx context.Context, data []byte, contentType string) (string, string, error)
}

func (f *fakeMediaService) SaveContent(
	ctx context.Context,
	data []byte,
	contentType string,
) (string, string, error) {
	return f.doFunc(ctx, data, contentType)
}

func (f *fakeMediaService) AccessCheck(ctx context.Context) error { return nil }

func (f *fakeMediaService) Enabled() bool { return f.enabled }

func (f *fakeMediaService) DownloadContent(
	ctx context.Context,
	id string,
) (io.ReadCloser, string, error) {
	return nil, "", nil
}

func TestDecodeBase64(t *testing.T) {
	t.Run("パディングを含むデータは実際の長さに切り詰められる", func(t *testing.T) {
		encoded := base64.URLEncoding.EncodeToString([]byte("hi"))
		raw := decodeBase64([]byte(encoded))
		require.Equal(t, []byte("hi"), raw)
	})

	t.Run("base64ではないデータはそのまま返す", func(t *testing.T) {
		v := []byte("not-valid-base64!!!")
		raw := decodeBase64(v)
		require.Equal(t, v, raw)
	})
}

func TestMediaUpload_Do(t *testing.T) {
	t.Run("base64データはデコードしてから委譲する", func(t *testing.T) {
		raw := []byte("raw-image-bytes")
		encoded := []byte(base64.URLEncoding.EncodeToString(raw))

		var gotData []byte
		service := &fakeMediaService{
			doFunc: func(ctx context.Context, data []byte, contentType string) (string, string, error) {
				gotData = data
				return "id", "https://example.com/id", nil
			},
		}
		u, _ := url.Parse("https://example.com")
		uploader := NewContentManagementService(u, service)
		id, url, err := uploader.SaveContent(context.Background(), encoded, "image/png")
		require.NoError(t, err)
		require.Equal(t, "id", id)
		require.Equal(t, "https://example.com/id", url)
		require.Equal(t, raw, gotData)
	})

	t.Run("base64ではないデータはそのまま委譲する", func(t *testing.T) {
		raw := []byte("not-valid-base64!!!")

		var gotData []byte
		service := &fakeMediaService{
			doFunc: func(ctx context.Context, data []byte, contentType string) (string, string, error) {
				gotData = data
				return "id", "https://example.com/id", nil
			},
		}

		u, _ := url.Parse("https://example.com")
		uploader := NewContentManagementService(u, service)
		_, _, err := uploader.SaveContent(context.Background(), raw, "image/png")
		require.NoError(t, err)
		require.Equal(t, raw, gotData)
	})
}
