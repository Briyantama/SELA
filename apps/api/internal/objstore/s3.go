package objstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// S3Config locates an S3-compatible bucket.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	PathStyle       bool
}

// S3 is a Store backed by an S3-compatible service (AWS S3, MinIO, ...).
type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewS3 builds the client. It does not contact the service.
func NewS3(cfg S3Config) (*S3, error) {
	var missing []string
	for _, field := range []struct{ name, value string }{
		{"endpoint", cfg.Endpoint}, {"region", cfg.Region}, {"bucket", cfg.Bucket},
		{"access key id", cfg.AccessKeyID}, {"secret access key", cfg.SecretAccessKey},
	} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("objstore: missing S3 %s", strings.Join(missing, ", "))
	}
	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: cfg.PathStyle,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		// Only add checksums when an operation requires them: pre-signed uploads come from browsers
		// that cannot compute them, and S3-compatible services differ in checksum support.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &S3{client: client, presign: s3.NewPresignClient(client), bucket: cfg.Bucket}, nil
}

func (s *S3) PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (PresignedRequest, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return PresignedRequest{}, fmt.Errorf("presign put: %w", err)
	}
	headers := map[string]string{
		"Content-Type":   contentType,
		"Content-Length": strconv.FormatInt(size, 10),
	}
	for name, values := range req.SignedHeader {
		canonical := http.CanonicalHeaderKey(name)
		if canonical == "Host" || len(values) == 0 {
			continue
		}
		if _, set := headers[canonical]; !set {
			headers[canonical] = values[0]
		}
	}
	return PresignedRequest{Method: req.Method, URL: req.URL, Headers: headers, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (s *S3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return req.URL, nil
}

func (s *S3) Head(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return ObjectInfo{}, classify("head", err)
	}
	return ObjectInfo{Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType)}, nil
}

func (s *S3) Get(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, classify("get", err)
	}
	defer func() { _ = out.Body.Close() }()
	if aws.ToInt64(out.ContentLength) > maxBytes {
		return nil, ErrTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(out.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, ErrTooLarge
	}
	return body, nil
}

func (s *S3) Put(ctx context.Context, key, contentType string, body []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(int64(len(body))),
		Body:          bytes.NewReader(body),
	})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// classify maps the service's "no such object" answers to ErrNotFound.
func classify(op string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey":
			return ErrNotFound
		}
	}
	return fmt.Errorf("%s object: %w", op, err)
}
