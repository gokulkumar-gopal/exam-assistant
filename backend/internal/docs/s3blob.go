package docs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
)

// S3Blob is a Blob over one S3 bucket.
type S3Blob struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

var _ Blob = (*S3Blob)(nil)

// NewS3Blob builds an S3Blob. Request checksums are only calculated when required: the
// default CRC32 would otherwise be signed into presigned PUT URLs, which a browser upload
// does not send.
func NewS3Blob(cfg aws.Config, bucket string) *S3Blob {
	c := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &S3Blob{client: c, presign: s3.NewPresignClient(c), bucket: bucket}
}

// PresignPut signs Content-Type and Content-Length so S3 rejects any other type or size.
func (b *S3Blob) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (string, error) {
	r, err := b.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(b.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return r.URL, nil
}

func (b *S3Blob) PresignGet(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	r, err := b.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:              aws.String(b.bucket),
		Key:                 aws.String(key),
		ResponseContentType: aws.String(contentType),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return r.URL, nil
}

func (b *S3Blob) Head(ctx context.Context, key string) (int64, error) {
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		if isNotFound(err) {
			return 0, store.ErrNotFound
		}
		return 0, fmt.Errorf("head object: %w", err)
	}
	return aws.ToInt64(out.ContentLength), nil
}

func (b *S3Blob) Get(ctx context.Context, key string, max int64) ([]byte, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)})
	if err != nil {
		if isNotFound(err) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(io.LimitReader(out.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("docs: object %q is larger than %d bytes", key, max)
	}
	return data, nil
}

func (b *S3Blob) Delete(ctx context.Context, key string) error {
	if _, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key)}); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func isNotFound(err error) bool {
	var nf *types.NotFound
	var nk *types.NoSuchKey
	if errors.As(err, &nf) || errors.As(err, &nk) {
		return true
	}
	var re *awshttp.ResponseError
	return errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound
}
