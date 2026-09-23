package objstore_test

import (
	"context"
	"testing"

	"github.com/Briyantama/SELA/internal/objstore"
)

func TestMemory_satisfiesTheStoreContract(t *testing.T) {
	runContract(t, objstore.NewMemory(), "contract")
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
