package storage

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"go.opentelemetry.io/otel/attribute"
)

const defaultURLExpiration = 1 * time.Hour

type S3API interface {
	UploadObject(ctx context.Context, input *transfermanager.UploadObjectInput) (*transfermanager.UploadObjectOutput, error)
	PresignGetObject(ctx context.Context, params *s3.GetObjectInput, expires time.Duration) (string, error)
	AccessCheck(ctx context.Context, bucket string) error
}

type s3Uploader struct {
	api       S3API
	bucket    string
	keyPrefix string
}

func NewS3Uploader(api S3API, bucketName, keyPrefix string) MediaUploadService {
	return &s3Uploader{
		api:       api,
		bucket:    bucketName,
		keyPrefix: keyPrefix,
	}
}

func (u *s3Uploader) Do(ctx context.Context, data []byte, contentType string) (_ string, _ string, rErr error) {
	mtype := mimetype.Detect(data)
	extensions := mtype.Extension()
	id := uuid.Must(uuid.NewV7()).String()

	bucket := aws.String(u.bucket)
	key := aws.String(fmt.Sprintf("%s/%s%s", u.keyPrefix, id, extensions))

	trace.StartSpan(ctx, "uploader/s3/Upload", attribute.String("key", id))
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

func (u *s3Uploader) AccessCheck(ctx context.Context) error {
	return u.api.AccessCheck(ctx, u.bucket)
}

func (u *s3Uploader) Enabled() bool { return true }
