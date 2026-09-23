package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store is the Redis-backed state behind the OTP flow and sessions.
// Keys are derived from keyed hashes, so no email address, code or token is stored in plaintext.
type Store interface {
	// CountWindow increments a fixed-window counter, starting the window on first use,
	// and returns the new count and the time left in the window.
	CountWindow(ctx context.Context, key string, window time.Duration) (count int64, remaining time.Duration, err error)
	// LockRemaining reports how long an address stays locked; zero means not locked.
	LockRemaining(ctx context.Context, emailID string, fallback time.Duration) (time.Duration, error)
	Lock(ctx context.Context, emailID string, d time.Duration) error

	// SaveCode replaces any outstanding code for the address and resets its attempt counter.
	SaveCode(ctx context.Context, emailID, mac string, ttl time.Duration) error
	LoadCode(ctx context.Context, emailID string) (mac string, found bool, err error)
	DeleteCode(ctx context.Context, emailID string) error
	// ConsumeCode atomically removes the code and reports whether this caller removed it.
	ConsumeCode(ctx context.Context, emailID string) (consumed bool, err error)
	// RecordAttempt counts a verification attempt for the current code.
	RecordAttempt(ctx context.Context, emailID string, ttl time.Duration) (int64, error)

	SaveSession(ctx context.Context, tokenID, hostID string, ttl time.Duration) error
	LoadSession(ctx context.Context, tokenID string) (hostID string, found bool, err error)
}

// counterScript increments a counter and guarantees it carries an expiry, atomically.
var counterScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {count, ttl}
`)

// RedisStore implements Store on Redis.
type RedisStore struct {
	rdb redis.Cmdable
}

// NewRedisStore returns a Store backed by the given Redis client.
func NewRedisStore(rdb redis.Cmdable) *RedisStore {
	return &RedisStore{rdb: rdb}
}

func otpKey(emailID string) string      { return "otp:" + emailID }
func attemptsKey(emailID string) string { return "attempts:" + emailID }
func lockKey(emailID string) string     { return "lock:" + emailID }
func sessionKey(tokenID string) string  { return "session:" + tokenID }

func (s *RedisStore) CountWindow(ctx context.Context, key string, window time.Duration) (int64, time.Duration, error) {
	res, err := counterScript.Run(ctx, s.rdb, []string{key}, window.Milliseconds()).Slice()
	if err != nil {
		return 0, 0, fmt.Errorf("count %s: %w", key, err)
	}
	if len(res) != 2 {
		return 0, 0, fmt.Errorf("count %s: unexpected script result %v", key, res)
	}
	count, okCount := res[0].(int64)
	ttlMs, okTTL := res[1].(int64)
	if !okCount || !okTTL {
		return 0, 0, fmt.Errorf("count %s: unexpected script result types %T, %T", key, res[0], res[1])
	}
	return count, time.Duration(ttlMs) * time.Millisecond, nil
}

func (s *RedisStore) LockRemaining(ctx context.Context, emailID string, fallback time.Duration) (time.Duration, error) {
	d, err := s.rdb.PTTL(ctx, lockKey(emailID)).Result()
	if err != nil {
		return 0, fmt.Errorf("read lock: %w", err)
	}
	switch {
	case d == -2: // no such key
		return 0, nil
	case d < 0: // key without expiry; should not happen, treat as locked for the full duration
		return fallback, nil
	default:
		return d, nil
	}
}

func (s *RedisStore) Lock(ctx context.Context, emailID string, d time.Duration) error {
	if err := s.rdb.Set(ctx, lockKey(emailID), "1", d).Err(); err != nil {
		return fmt.Errorf("set lock: %w", err)
	}
	return nil
}

func (s *RedisStore) SaveCode(ctx context.Context, emailID, mac string, ttl time.Duration) error {
	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, otpKey(emailID), mac, ttl)
	pipe.Del(ctx, attemptsKey(emailID))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("save code: %w", err)
	}
	return nil
}

func (s *RedisStore) LoadCode(ctx context.Context, emailID string) (string, bool, error) {
	mac, err := s.rdb.Get(ctx, otpKey(emailID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load code: %w", err)
	}
	return mac, true, nil
}

func (s *RedisStore) DeleteCode(ctx context.Context, emailID string) error {
	if err := s.rdb.Del(ctx, otpKey(emailID)).Err(); err != nil {
		return fmt.Errorf("delete code: %w", err)
	}
	return nil
}

func (s *RedisStore) ConsumeCode(ctx context.Context, emailID string) (bool, error) {
	n, err := s.rdb.Del(ctx, otpKey(emailID)).Result()
	if err != nil {
		return false, fmt.Errorf("consume code: %w", err)
	}
	return n == 1, nil
}

func (s *RedisStore) RecordAttempt(ctx context.Context, emailID string, ttl time.Duration) (int64, error) {
	count, _, err := s.CountWindow(ctx, attemptsKey(emailID), ttl)
	return count, err
}

func (s *RedisStore) SaveSession(ctx context.Context, tokenID, hostID string, ttl time.Duration) error {
	if err := s.rdb.Set(ctx, sessionKey(tokenID), hostID, ttl).Err(); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

func (s *RedisStore) LoadSession(ctx context.Context, tokenID string) (string, bool, error) {
	hostID, err := s.rdb.Get(ctx, sessionKey(tokenID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load session: %w", err)
	}
	return hostID, true, nil
}
