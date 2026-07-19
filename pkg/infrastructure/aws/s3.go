package aws

import (
	"context"
	"os"
	"strconv"
	"time"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3ClientImpl struct {
	s3Client       *s3.Client
	transferClient *transfermanager.Client
	presignClient  *s3.PresignClient
}

func NewS3Client(cfg awsSDK.Config) *S3ClientImpl {
	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle, _ = strconv.ParseBool(os.Getenv("AWS_S3_FORCE_PATH_STYLE"))
	})
	transferClient := transfermanager.New(s3Client)
	presignClient := s3.NewPresignClient(s3Client)

	return &S3ClientImpl{
		s3Client:       s3Client,
		transferClient: transferClient,
		presignClient:  presignClient,
	}
}

func (c *S3ClientImpl) UploadObject(
	ctx context.Context,
	input *transfermanager.UploadObjectInput,
) (*transfermanager.UploadObjectOutput, error) {
	output, err := c.transferClient.UploadObject(ctx, input)
	if err != nil {
		return nil, err
	}
	return output, nil
}

func (c *S3ClientImpl) PresignGetObject(
	ctx context.Context,
	params *s3.GetObjectInput,
	expires time.Duration,
) (string, error) {
	request, err := c.presignClient.PresignGetObject(ctx, params, func(opts *s3.PresignOptions) {
		opts.Expires = expires
	})
	if err != nil {
		return "", err
	}
	return request.URL, nil
}

func (c *S3ClientImpl) AccessCheck(ctx context.Context, bucket string) error {
	_, err := c.s3Client.ListObjects(ctx, &s3.ListObjectsInput{
		Bucket:  awsSDK.String(bucket),
		MaxKeys: awsSDK.Int32(1),
	})
	return err
}
