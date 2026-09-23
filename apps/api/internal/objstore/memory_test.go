package objstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/Briyantama/SELA/internal/objstore"
)

func TestMemory_satisfiesTheStoreContract(t *testing.T) {
	runContract(t, objstore.NewMemory(), "contract")
}

func TestMemory_uploadBehavesLikeABrowserPutAgainstTheSignature(t *testing.T) {
	// Arrange
	store := objstore.NewMemory()
	ctx := context.Background()
	req, err := store.PresignPut(ctx, "incoming/e/m", "image/jpeg", 3, time.Minute)
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}

	// Act
	wrongSize := store.Upload(req, "image/jpeg", []byte("four"))
	wrongType := store.Upload(req, "image/png", []byte("abc"))
	ok := store.Upload(req, "image/jpeg", []byte("abc"))
	info, headErr := store.Head(ctx, store.KeyOf(req))

	// Assert
	if wrongSize == nil || wrongType == nil {
		t.Fatalf("Upload accepted an unsigned size or type: %v, %v", wrongSize, wrongType)
	}
	if ok != nil || headErr != nil || info.Size != 3 {
		t.Fatalf("Upload = %v, Head = %+v, %v", ok, info, headErr)
	}
	if store.KeyOf(req) != "incoming/e/m" {
		t.Fatalf("KeyOf = %q, want incoming/e/m", store.KeyOf(req))
	}
}

func TestMemory_listsKeysForAssertions(t *testing.T) {
	// Arrange
	store := objstore.NewMemory()
	ctx := context.Background()
	for _, key := range []string{"b/2", "a/1"} {
		if err := store.Put(ctx, key, "image/png", []byte("x")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	// Act
	keys := store.Keys()

	// Assert
	if len(keys) != 2 || keys[0] != "a/1" || keys[1] != "b/2" {
		t.Fatalf("Keys = %v, want [a/1 b/2]", keys)
	}
}
