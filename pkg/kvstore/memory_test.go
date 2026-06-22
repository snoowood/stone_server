package kvstore

import (
	"context"
	"testing"
	"time"
)

// XC-3: sweep evicts expired kv entries and reports accurate swept/remaining counts,
// leaving non-expired (and no-TTL) keys intact.
func TestMemStore_Sweep(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	if err := m.Set(ctx, "expiring", "v", 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := m.Set(ctx, "permanent", "v", 0); err != nil { // 0 = no expiry
		t.Fatal(err)
	}
	if err := m.Set(ctx, "longlived", "v", time.Hour); err != nil {
		t.Fatal(err)
	}

	time.Sleep(40 * time.Millisecond) // let "expiring" pass its TTL

	swept, remaining := m.sweep()
	if swept != 1 {
		t.Errorf("swept = %d, want 1 (the expired key)", swept)
	}
	if remaining != 2 {
		t.Errorf("remaining = %d, want 2 (permanent + longlived)", remaining)
	}
	if _, err := m.Get(ctx, "expiring"); err != ErrNotFound {
		t.Errorf("expired key should be gone after sweep, got err=%v", err)
	}
	if v, _ := m.Get(ctx, "permanent"); v != "v" {
		t.Error("no-TTL key must survive sweep")
	}
	if v, _ := m.Get(ctx, "longlived"); v != "v" {
		t.Error("unexpired key must survive sweep")
	}
}

// StartSweeper must evict expired keys on its ticker and stop cleanly on ctx cancel.
// We read the raw map size (not Get, which would lazily evict and mask the sweeper).
func TestMemStore_StartSweeper_EvictsAndStops(t *testing.T) {
	m := NewMemStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = m.Set(context.Background(), "expiring", "v", 10*time.Millisecond)
	m.StartSweeper(ctx, 15*time.Millisecond)

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		m.mu.Lock()
		n := len(m.kv)
		m.mu.Unlock()
		if n == 0 {
			break // sweeper evicted the expired key
		}
		if time.Now().After(deadline) {
			t.Fatal("sweeper did not evict the expired key within the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel() // sweeper goroutine must return without panicking
}
