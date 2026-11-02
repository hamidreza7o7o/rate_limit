// Package ratelimit limits how many times a key may be used in a fixed time window.
//
// Fixed Window Counter splits time into back-to-back windows of a constant length.
// Every request in the same window shares one counter. The counter starts over when
// the next window begins. Redis stores the counter so several processes share one limit.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// incrWithTTL increments the window counter and, on the first hit, sets its expiry.
// Both steps run in one script so a crash cannot leave a counter that never expires.
var incrWithTTL = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
    redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return count
`)

// Decision is the outcome of one Allow call.
type Decision struct {
	Allowed   bool
	Limit     int64
	Remaining int64
	// Reset is the time when the current window ends and the counter returns to zero.
	Reset time.Time
}

// Limiter is a fixed-window counter backed by Redis.
type Limiter struct {
	rdb    *redis.Client
	limit  int64
	window time.Duration
	now    func() time.Time
}

// New returns a limiter that allows limit requests per window for each key.
// window is the fixed window length, for example time.Minute.
func New(rdb *redis.Client, limit int64, window time.Duration) (*Limiter, error) {
	if rdb == nil {
		return nil, errors.New("redis client is nil")
	}
	if limit < 1 {
		return nil, errors.New("limit must be at least 1")
	}
	if window < time.Millisecond {
		return nil, errors.New("window must be at least 1ms")
	}
	return &Limiter{
		rdb:    rdb,
		limit:  limit,
		window: window,
		now:    time.Now,
	}, nil
}

// Allow records one request for key and reports whether it is inside the limit.
// key is any stable identity, such as a user id or client IP.
func (l *Limiter) Allow(ctx context.Context, key string) (Decision, error) {
	if key == "" {
		return Decision{}, errors.New("key is empty")
	}

	now := l.now().UTC()
	start := now.Truncate(l.window)
	reset := start.Add(l.window)

	ttl := reset.Sub(now)
	ttlMs := ttl.Milliseconds()
	if ttlMs < 1 {
		ttlMs = 1
	}

	redisKey := fmt.Sprintf("ratelimit:%s:%d", key, start.UnixNano())
	count, err := incrWithTTL.Run(ctx, l.rdb, []string{redisKey}, ttlMs).Int64()
	if err != nil {
		return Decision{}, fmt.Errorf("fixed window: %w", err)
	}

	remaining := l.limit - count
	if remaining < 0 {
		remaining = 0
	}

	return Decision{
		Allowed:   count <= l.limit,
		Limit:     l.limit,
		Remaining: remaining,
		Reset:     reset,
	}, nil
}
