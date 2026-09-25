package objstore_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Briyantama/SELA/internal/objstore"
)

// runContract checks the behaviour every Store must share, under a key prefix unique to the caller.
func runContract(t *testing.T, store objstore.Store, prefix string) {
	t.Helper()
	ctx := context.Background()

	t.Run("put then head and get", func(t *testing.T) {
		// Arrange
		key := prefix + "/put-get"
		body := []byte("hello sela")

		// Act
		if err := store.Put(ctx, key, "image/png", body); err != nil {
			t.Fatalf("Put: %v", err)
		}
		info, headErr := store.Head(ctx, key)
		got, getErr := store.Get(ctx, key, 1024)

		// Assert
		if headErr != nil || info.Size != int64(len(body)) || info.ContentType != "image/png" {
			t.Fatalf("Head = %+v, %v; want size %d image/png", info, headErr, len(body))
		}
		if getErr != nil || !bytes.Equal(got, body) {
			t.Fatalf("Get = %q, %v; want %q", got, getErr, body)
		}
	})

	t.Run("missing object is ErrNotFound", func(t *testing.T) {
		// Act
		_, headErr := store.Head(ctx, prefix+"/missing")
		_, getErr := store.Get(ctx, prefix+"/missing", 1024)

		// Assert
		if !errors.Is(headErr, objstore.ErrNotFound) || !errors.Is(getErr, objstore.ErrNotFound) {
			t.Fatalf("Head err = %v, Get err = %v; want ErrNotFound", headErr, getErr)
		}
	})

	t.Run("get refuses an object over the byte limit", func(t *testing.T) {
		// Arrange
		key := prefix + "/too-large"
		if err := store.Put(ctx, key, "image/jpeg", bytes.Repeat([]byte{1}, 64)); err != nil {
			t.Fatalf("Put: %v", err)
		}

		// Act
		_, err := store.Get(ctx, key, 63)

		// Assert
		if !errors.Is(err, objstore.ErrTooLarge) {
			t.Fatalf("Get err = %v, want ErrTooLarge", err)
		}
	})

	t.Run("delete is idempotent", func(t *testing.T) {
		// Arrange
		key := prefix + "/delete"
		if err := store.Put(ctx, key, "image/webp", []byte("x")); err != nil {
			t.Fatalf("Put: %v", err)
		}

		// Act
		first := store.Delete(ctx, key)
		second := store.Delete(ctx, key)
		_, headErr := store.Head(ctx, key)

		// Assert
		if first != nil || second != nil {
			t.Fatalf("Delete = %v then %v, want nil twice", first, second)
		}
		if !errors.Is(headErr, objstore.ErrNotFound) {
			t.Fatalf("Head after delete = %v, want ErrNotFound", headErr)
		}
	})

	t.Run("presigned put names the method, headers and expiry", func(t *testing.T) {
		// Arrange
		before := time.Now()

		// Act
		req, err := store.PresignPut(ctx, prefix+"/presigned", "image/jpeg", 123, 10*time.Minute)

		// Assert
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		if req.Method != "PUT" || req.URL == "" {
			t.Fatalf("PresignPut = %+v, want a PUT URL", req)
		}
		if req.Headers["Content-Type"] != "image/jpeg" || req.Headers["Content-Length"] != "123" {
			t.Fatalf("headers = %v, want the signed Content-Type and Content-Length", req.Headers)
		}
		if req.ExpiresAt.Before(before.Add(9*time.Minute)) || req.ExpiresAt.After(before.Add(11*time.Minute)) {
			t.Fatalf("ExpiresAt = %v, want about 10 minutes from now", req.ExpiresAt)
		}
	})

	t.Run("presigned get returns a url", func(t *testing.T) {
		// Act
		url, err := store.PresignGet(ctx, prefix+"/put-get", time.Minute)

		// Assert
		if err != nil || url == "" {
			t.Fatalf("PresignGet = %q, %v", url, err)
		}
	})
}
