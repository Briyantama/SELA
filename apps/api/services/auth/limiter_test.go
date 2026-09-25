package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Briyantama/SELA/services/auth"
)

func newLimiter(t *testing.T) (*auth.WindowLimiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return auth.NewWindowLimiter(auth.NewRedisStore(rdb)), mr
}

func TestWindowLimiter_allowsUpToTheLimitThenBlocksWithRetryAfter(t *testing.T) {
	// Arrange
	l, _ := newLimiter(t)
	ctx := context.Background()

	// Act + Assert
	for i := 1; i <= 3; i++ {
		ok, _, err := l.Allow(ctx, "guest-session", "203.0.113.7", 3, time.Minute)
		if err != nil || !ok {
			t.Fatalf("call %d: ok=%v err=%v, want allowed", i, ok, err)
		}
	}
	ok, retry, err := l.Allow(ctx, "guest-session", "203.0.113.7", 3, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("4th call allowed, want blocked")
	}
	if retry <= 0 || retry > time.Minute {
		t.Errorf("retryAfter = %v, want within (0, 1m]", retry)
	}
}

func TestWindowLimiter_isolatesSubjectsAndScopes(t *testing.T) {
	// Arrange
	l, _ := newLimiter(t)
	ctx := context.Background()
	_, _, _ = l.Allow(ctx, "guest-session", "a", 1, time.Minute)

	// Act
	otherSubject, _, _ := l.Allow(ctx, "guest-session", "b", 1, time.Minute)
	otherScope, _, _ := l.Allow(ctx, "upload", "a", 1, time.Minute)

	// Assert
	if !otherSubject || !otherScope {
		t.Errorf("otherSubject=%v otherScope=%v, want both allowed", otherSubject, otherScope)
	}
}

func TestWindowLimiter_allowsAgainAfterTheWindowExpires(t *testing.T) {
	// Arrange
	l, mr := newLimiter(t)
	ctx := context.Background()
	_, _, _ = l.Allow(ctx, "upload", "s", 1, time.Minute)
	if ok, _, _ := l.Allow(ctx, "upload", "s", 1, time.Minute); ok {
		t.Fatal("2nd call allowed, want blocked")
	}

	// Act
	mr.FastForward(time.Minute + time.Second)
	ok, _, err := l.Allow(ctx, "upload", "s", 1, time.Minute)

	// Assert
	if err != nil || !ok {
		t.Errorf("after window: ok=%v err=%v, want allowed", ok, err)
	}
}

func TestWindowLimiter_neverStoresTheRawSubject(t *testing.T) {
	// Arrange
	l, mr := newLimiter(t)

	// Act
	_, _, _ = l.Allow(context.Background(), "guest-session", "203.0.113.7", 5, time.Minute)

	// Assert
	keys := mr.Keys()
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "rl:guest-session:") {
		t.Fatalf("keys = %v, want one rl:guest-session:* key", keys)
	}
	if strings.Contains(keys[0], "203.0.113.7") {
		t.Errorf("key %q contains the raw subject", keys[0])
	}
}
