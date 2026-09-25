package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Counter is the fixed-window counter a WindowLimiter needs; *RedisStore implements it.
type Counter interface {
	CountWindow(ctx context.Context, key string, window time.Duration) (count int64, remaining time.Duration, err error)
}

// WindowLimiter applies per-subject fixed-window limits (FR-SEC.9) on top of the same
// atomic Redis counter the OTP flow uses.
type WindowLimiter struct {
	counter Counter
}

// NewWindowLimiter returns a limiter backed by the given counter.
func NewWindowLimiter(c Counter) *WindowLimiter {
	return &WindowLimiter{counter: c}
}

// Allow counts one hit for subject in scope and reports whether it is within limit.
// When it is not, retryAfter is the time left in the window. The subject is hashed, so
// Redis never holds a raw address or session id.
func (l *WindowLimiter) Allow(ctx context.Context, scope, subject string, limit int, window time.Duration) (ok bool, retryAfter time.Duration, err error) {
	sum := sha256.Sum256([]byte(subject))
	key := "rl:" + scope + ":" + hex.EncodeToString(sum[:])
	count, remaining, err := l.counter.CountWindow(ctx, key, window)
	if err != nil {
		return false, 0, err
	}
	if count > int64(limit) {
		return false, remaining, nil
	}
	return true, 0, nil
}
