package media

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// GuestStore holds anonymous guest sessions and per-session shot counters (FSD 2.4). Every key has a TTL.
type GuestStore interface {
	SaveSession(ctx context.Context, tokenID string, g Guest, ttl time.Duration) error
	LoadSession(ctx context.Context, tokenID string) (Guest, bool, error)
	// Reserve takes one shot atomically, returning what is left. When the counter is missing it is
	// seeded from seed(), the allowance derived from the durable media rows.
	Reserve(ctx context.Context, g Guest, seed func() (int, error), ttl time.Duration) (int, bool, error)
	// Release gives one shot back, never above limit.
	Release(ctx context.Context, g Guest, limit int) error
}

// RedisGuests is the Redis GuestStore.
type RedisGuests struct {
	rdb redis.Cmdable
}

// NewRedisGuests returns a GuestStore on rdb.
func NewRedisGuests(rdb redis.Cmdable) *RedisGuests {
	return &RedisGuests{rdb: rdb}
}

func sessionKey(tokenID string) string { return "guest:" + tokenID }
func quotaKey(g Guest) string          { return "quota:" + g.EventID + ":" + g.SessionID }

func (r *RedisGuests) SaveSession(ctx context.Context, tokenID string, g Guest, ttl time.Duration) error {
	if err := r.rdb.Set(ctx, sessionKey(tokenID), g.EventID+"|"+g.SessionID, ttl).Err(); err != nil {
		return fmt.Errorf("save guest session: %w", err)
	}
	return nil
}

func (r *RedisGuests) LoadSession(ctx context.Context, tokenID string) (Guest, bool, error) {
	value, err := r.rdb.Get(ctx, sessionKey(tokenID)).Result()
	if errors.Is(err, redis.Nil) {
		return Guest{}, false, nil
	}
	if err != nil {
		return Guest{}, false, fmt.Errorf("load guest session: %w", err)
	}
	eventID, sessionID, ok := strings.Cut(value, "|")
	if !ok {
		return Guest{}, false, nil
	}
	return Guest{EventID: eventID, SessionID: sessionID}, true, nil
}

// reserveScript takes one shot if any is left. ARGV[1] is the seed ("" = do not seed) and ARGV[2] the
// TTL in milliseconds. It returns what is left, -1 when exhausted, or -2 when the key is missing and
// no seed was given.
var reserveScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if not v then
  if ARGV[1] == '' then return -2 end
  v = ARGV[1]
  redis.call('SET', KEYS[1], v, 'PX', ARGV[2])
end
if tonumber(v) <= 0 then return -1 end
local n = redis.call('DECR', KEYS[1])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return n
`)

const (
	reserveExhausted = -1
	reserveUnseeded  = -2
)

func (r *RedisGuests) Reserve(ctx context.Context, g Guest, seed func() (int, error), ttl time.Duration) (int, bool, error) {
	key := quotaKey(g)
	ttlMillis := ttl.Milliseconds()
	left, err := reserveScript.Run(ctx, r.rdb, []string{key}, "", ttlMillis).Int()
	if err != nil {
		return 0, false, fmt.Errorf("reserve shot: %w", err)
	}
	if left == reserveUnseeded {
		allowance, err := seed()
		if err != nil {
			return 0, false, err
		}
		// Racing reservations may both seed; the script only seeds a key that is still missing.
		left, err = reserveScript.Run(ctx, r.rdb, []string{key}, strconv.Itoa(allowance), ttlMillis).Int()
		if err != nil {
			return 0, false, fmt.Errorf("reserve shot: %w", err)
		}
	}
	if left == reserveExhausted {
		return 0, false, nil
	}
	return left, true, nil
}

// releaseScript returns one shot to an existing counter, capped at ARGV[1]. A missing counter is left
// alone: the next reservation reseeds it from the media rows, which no longer count the failed shot.
var releaseScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if not v then return 0 end
if tonumber(v) < tonumber(ARGV[1]) then return redis.call('INCR', KEYS[1]) end
return tonumber(v)
`)

func (r *RedisGuests) Release(ctx context.Context, g Guest, limit int) error {
	if err := releaseScript.Run(ctx, r.rdb, []string{quotaKey(g)}, limit).Err(); err != nil {
		return fmt.Errorf("release shot: %w", err)
	}
	return nil
}
