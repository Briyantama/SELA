package objstore_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Briyantama/SELA/internal/objstore"
)

// s3Store connects to the S3-compatible test bucket named by TEST_S3_* (MinIO in development), or
// skips the test when TEST_S3_ENDPOINT is unset. scripts/check.sh sets it.
func s3Store(t *testing.T) (objstore.Store, string) {
	t.Helper()
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_S3_ENDPOINT not set; skipping the S3 adapter test")
	}
	store, err := objstore.NewS3(objstore.S3Config{
		Endpoint:        endpoint,
		Region:          envOr("TEST_S3_REGION", "us-east-1"),
		Bucket:          envOr("TEST_S3_BUCKET", "sela-media-dev"),
		AccessKeyID:     os.Getenv("TEST_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("TEST_S3_SECRET_ACCESS_KEY"),
		PathStyle:       true,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random prefix: %v", err)
	}
	return store, "objstore-test/" + hex.EncodeToString(suffix)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func TestS3_satisfiesTheStoreContract(t *testing.T) {
	store, prefix := s3Store(t)
	runContract(t, store, prefix)
}

func TestNewS3_rejectsAnIncompleteConfig(t *testing.T) {
	// Act
	_, err := objstore.NewS3(objstore.S3Config{Endpoint: "http://127.0.0.1:9000"})

	// Assert
	if err == nil {
		t.Fatal("NewS3 with no bucket or credentials returned nil")
	}
}

func put(t *testing.T, req objstore.PresignedRequest, body []byte, override map[string]string) *http.Response {
	t.Helper()
	httpReq, err := http.NewRequest(req.Method, req.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for name, value := range req.Headers {
		httpReq.Header.Set(name, value)
	}
	for name, value := range override {
		httpReq.Header.Set(name, value)
	}
	httpReq.ContentLength = int64(len(body))
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestS3_presignedPutAcceptsExactlyTheSignedUpload(t *testing.T) {
	// Arrange
	store, prefix := s3Store(t)
	ctx := context.Background()
	key := prefix + "/signed-upload"
	body := []byte("a signed upload body")
	req, err := store.PresignPut(ctx, key, "image/jpeg", int64(len(body)), time.Minute)
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	// Act
	resp := put(t, req, body, nil)
	info, headErr := store.Head(ctx, key)

	// Assert
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}
	if headErr != nil || info.Size != int64(len(body)) || info.ContentType != "image/jpeg" {
		t.Fatalf("Head = %+v, %v", info, headErr)
	}
}

func TestS3_presignedPutRejectsADifferentSizeOrType(t *testing.T) {
	store, prefix := s3Store(t)
	ctx := context.Background()
	body := []byte("twenty bytes of data")

	tests := []struct {
		name     string
		signed   int64
		override map[string]string
	}{
		{"larger body than signed", int64(len(body)) - 5, map[string]string{"Content-Length": strconv.Itoa(len(body))}},
		{"different content type", int64(len(body)), map[string]string{"Content-Type": "text/html"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			key := prefix + "/rejected-" + tc.name
			req, err := store.PresignPut(ctx, key, "image/jpeg", tc.signed, time.Minute)
			if err != nil {
				t.Fatalf("PresignPut: %v", err)
			}

			// Act
			resp := put(t, req, body, tc.override)

			// Assert
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("PUT status = 200, want a rejection")
			}
			if _, err := store.Head(ctx, key); err == nil {
				t.Fatal("the rejected upload was stored")
			}
		})
	}
}

func TestS3_presignedGetServesTheObject(t *testing.T) {
	// Arrange
	store, prefix := s3Store(t)
	ctx := context.Background()
	key := prefix + "/served"
	if err := store.Put(ctx, key, "image/png", []byte("png bytes")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	url, err := store.PresignGet(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}

	// Act
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)

	// Assert
	if resp.StatusCode != http.StatusOK || string(got) != "png bytes" {
		t.Fatalf("GET = %d %q, want 200 \"png bytes\"", resp.StatusCode, got)
	}
}
