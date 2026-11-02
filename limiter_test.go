package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestLimiter(t *testing.T, limit int64, window time.Duration) (*Limiter, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	lim, err := New(rdb, limit, window)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return lim, srv
}

func TestAllowWithinLimit(t *testing.T) {
	lim, _ := newTestLimiter(t, 3, time.Minute)
	ctx := context.Background()

	for i := int64(1); i <= 3; i++ {
		d, err := lim.Allow(ctx, "user-1")
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if !d.Allowed {
			t.Fatalf("request %d was rejected", i)
		}
		if d.Remaining != 3-i {
			t.Fatalf("remaining = %d, want %d", d.Remaining, 3-i)
		}
		if d.Limit != 3 {
			t.Fatalf("limit = %d, want 3", d.Limit)
		}
	}
}

func TestAllowOverLimit(t *testing.T) {
	lim, _ := newTestLimiter(t, 2, time.Minute)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		d, err := lim.Allow(ctx, "user-1")
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if !d.Allowed {
			t.Fatal("request inside the limit was rejected")
		}
	}

	d, err := lim.Allow(ctx, "user-1")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if d.Allowed {
		t.Fatal("request over the limit was allowed")
	}
	if d.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", d.Remaining)
	}
}

func TestKeysAreIndependent(t *testing.T) {
	lim, _ := newTestLimiter(t, 1, time.Minute)
	ctx := context.Background()

	first, err := lim.Allow(ctx, "alice")
	if err != nil {
		t.Fatalf("Allow alice: %v", err)
	}
	if !first.Allowed {
		t.Fatal("alice was rejected")
	}

	blocked, err := lim.Allow(ctx, "alice")
	if err != nil {
		t.Fatalf("Allow alice again: %v", err)
	}
	if blocked.Allowed {
		t.Fatal("alice's second request was allowed")
	}

	other, err := lim.Allow(ctx, "bob")
	if err != nil {
		t.Fatalf("Allow bob: %v", err)
	}
	if !other.Allowed {
		t.Fatal("bob was rejected because of alice")
	}
}

func TestWindowResets(t *testing.T) {
	lim, _ := newTestLimiter(t, 1, time.Minute)
	start := time.Date(2026, 11, 1, 12, 0, 30, 0, time.UTC)
	lim.now = func() time.Time { return start }
	ctx := context.Background()

	d, err := lim.Allow(ctx, "user-1")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !d.Allowed {
		t.Fatal("first request was rejected")
	}
	wantReset := time.Date(2026, 11, 1, 12, 1, 0, 0, time.UTC)
	if !d.Reset.Equal(wantReset) {
		t.Fatalf("reset = %s, want %s", d.Reset, wantReset)
	}

	blocked, err := lim.Allow(ctx, "user-1")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if blocked.Allowed {
		t.Fatal("second request in the same window was allowed")
	}

	lim.now = func() time.Time { return start.Add(time.Minute) }
	again, err := lim.Allow(ctx, "user-1")
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !again.Allowed {
		t.Fatal("request in the next window was rejected")
	}
	if again.Remaining != 0 {
		t.Fatalf("remaining = %d, want 0", again.Remaining)
	}
}

func TestRedisError(t *testing.T) {
	lim, srv := newTestLimiter(t, 1, time.Minute)
	srv.Close()

	_, err := lim.Allow(context.Background(), "user-1")
	if err == nil {
		t.Fatal("expected an error after redis stopped")
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	if _, err := New(nil, 1, time.Second); err == nil {
		t.Fatal("nil client was accepted")
	}
	if _, err := New(rdb, 0, time.Second); err == nil {
		t.Fatal("zero limit was accepted")
	}
	if _, err := New(rdb, 1, 0); err == nil {
		t.Fatal("zero window was accepted")
	}
	if _, err := New(rdb, 1, time.Second); err != nil {
		t.Fatalf("valid config: %v", err)
	}
}
