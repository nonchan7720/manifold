package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"go.opentelemetry.io/otel/attribute"
)

const defaultURLExpiration = 1 * time.Hour

type S3API interface {
	UploadObject(ctx context.Context, input *transfermanager.UploadObjectInput) (*transfermanager.UploadObjectOutput, error)
	PresignGetObject(ctx context.Context, params *s3.GetObjectInput, expires time.Duration) (string, error)
	AccessCheck(ctx context.Context, bucket string) error
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type s3Service struct {
	api       S3API
	bucket    string
	keyPrefix string
}

func NewS3Uploader(api S3API, bucketName, keyPrefix string) MediaService {
	return &s3Service{
		api:       api,
		bucket:    bucketName,
		keyPrefix: keyPrefix,
	}
}

func (u *s3Service) SaveContent(ctx context.Context, data []byte, contentType string) (_ string, _ string, rErr error) {
	id := uuid.Must(uuid.NewV7()).String()

	bucket := aws.String(u.bucket)
	key := aws.String(fmt.Sprintf("%s/%s", u.keyPrefix, id))

	trace.StartSpan(ctx, "MediaService/S3/Upload", attribute.String("key", id))
	defer func() { trace.EndSpan(ctx, rErr) }()
	_, err := u.api.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket:      bucket,
		Key:         key,
		Body:        bytes.NewBuffer(data),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to s3 upload(%s)", id)
	}
	url, err := u.api.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: bucket,
		Key:    key,
	}, defaultURLExpiration)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate pre-sign url(%s)", id)
	}
	return id, url, nil
}

func (u *s3Service) DownloadContent(ctx context.Context, id string) (_ io.ReadCloser, _ string, rErr error) {
	bucket := aws.String(u.bucket)
	key := aws.String(fmt.Sprintf("%s/%s", u.keyPrefix, id))

	trace.StartSpan(ctx, "MediaService/S3/DownloadContent", attribute.String("key", id))
	defer func() { trace.EndSpan(ctx, rErr) }()

	resp, err := u.api.GetObject(ctx, &s3.GetObjectInput{
		Bucket: bucket,
		Key:    key,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to get file content")
	}
	return resp.Body, aws.ToString(resp.ContentType), nil
}

func (u *s3Service) AccessCheck(ctx context.Context) error {
	return u.api.AccessCheck(ctx, u.bucket)
}

func (u *s3Service) Enabled() bool { return true }
