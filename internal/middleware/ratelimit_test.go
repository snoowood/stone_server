package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gensdeis/stone-server/pkg/kvstore"
)

// fakeKV embeds the KVStore interface (nil) and overrides only the methods
// incrRateLimit calls, so the SetNX-false → expired → IncrBy-recreated → Expire-fails
// race can be reproduced deterministically.
type fakeKV struct {
	kvstore.KVStore
	setNX     bool
	incr      int64
	expireErr error
	delCalled bool
}

func (f *fakeKV) SetNX(context.Context, string, string, time.Duration) (bool, error) {
	return f.setNX, nil
}
func (f *fakeKV) IncrBy(context.Context, string, int64) (int64, error) { return f.incr, nil }
func (f *fakeKV) Expire(context.Context, string, time.Duration) error  { return f.expireErr }
func (f *fakeKV) Del(context.Context, ...string) error                 { f.delCalled = true; return nil }

// SEC-3: the fixed-window counter increments correctly across the SetNX (first hit)
// and IncrBy (subsequent hits) paths, returning a contiguous 1,2,3,… sequence.
func TestIncrRateLimit_CountsAcrossSetNXAndIncr(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	const key = "ratelimit:test"
	const window = time.Minute

	for i := int64(1); i <= 5; i++ {
		count, err := incrRateLimit(ctx, kv, key, window)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if count != i {
			t.Fatalf("call %d: count = %d, want %d", i, count, i)
		}
	}

	// Independent keys must not share a counter.
	count, err := incrRateLimit(ctx, kv, "ratelimit:other", window)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("separate key: count = %d, want 1", count)
	}
}

// SEC-3: once the window TTL elapses the counter resets to 1 (fixed-window
// semantics preserved by the SetNX-with-TTL first hit).
func TestIncrRateLimit_ResetsAfterWindow(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	const key = "ratelimit:expiry"
	const window = 40 * time.Millisecond

	if c, _ := incrRateLimit(ctx, kv, key, window); c != 1 {
		t.Fatalf("first hit: count = %d, want 1", c)
	}
	if c, _ := incrRateLimit(ctx, kv, key, window); c != 2 {
		t.Fatalf("second hit: count = %d, want 2", c)
	}

	time.Sleep(window + 30*time.Millisecond) // let the window TTL expire

	if c, _ := incrRateLimit(ctx, kv, key, window); c != 1 {
		t.Fatalf("after window expiry: count = %d, want 1 (reset)", c)
	}
}

// SEC-3: the re-arm branch — SetNX returned false but the key had expired, so IncrBy
// re-created it without a TTL (count==1) and Expire then fails — must Del the poison
// key so the identifier isn't pinned at 429 forever.
func TestIncrRateLimit_DropsTTLlessKeyOnReArmFailure(t *testing.T) {
	f := &fakeKV{setNX: false, incr: 1, expireErr: errors.New("expire boom")}

	count, err := incrRateLimit(context.Background(), f, "ratelimit:race", time.Minute)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	if !f.delCalled {
		t.Error("expected Del to drop the TTL-less poison key after Expire failure")
	}
}
