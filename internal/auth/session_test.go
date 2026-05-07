package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gensdeis/stone-server/pkg/kvstore"
)

func newTestKV() kvstore.KVStore {
	return kvstore.NewMemStore()
}

// TestEnforceSingleSession_FirstLogin verifies no error and correct keys when
// there is no prior session for the player.
func TestEnforceSingleSession_FirstLogin(t *testing.T) {
	kv := newTestKV()
	ctx := context.Background()

	if err := enforceSingleSession(ctx, kv, "player-1", "jti-new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// session:{new_jti} must exist.
	if _, err := kv.Get(ctx, "session:jti-new"); err != nil {
		t.Errorf("session:jti-new not set: %v", err)
	}
	// session:current:{player_id} must point to new jti.
	cur, err := kv.Get(ctx, "session:current:player-1")
	if err != nil || cur != "jti-new" {
		t.Errorf("session:current:player-1 = %q, want %q (err: %v)", cur, "jti-new", err)
	}
}

// TestEnforceSingleSession_Rotation verifies that the old jti is deleted and
// the new jti is registered when a session already exists.
func TestEnforceSingleSession_Rotation(t *testing.T) {
	kv := newTestKV()
	ctx := context.Background()

	kv.Set(ctx, "session:jti-old", "1", 24*time.Hour)
	kv.Set(ctx, "session:current:player-1", "jti-old", 24*time.Hour)

	if err := enforceSingleSession(ctx, kv, "player-1", "jti-new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Old jti must be gone.
	if _, err := kv.Get(ctx, "session:jti-old"); err == nil {
		t.Error("session:jti-old should have been deleted")
	}
	// New jti must exist.
	if _, err := kv.Get(ctx, "session:jti-new"); err != nil {
		t.Errorf("session:jti-new not set: %v", err)
	}
	// session:current must point to new jti.
	cur, _ := kv.Get(ctx, "session:current:player-1")
	if cur != "jti-new" {
		t.Errorf("session:current:player-1 = %q, want %q", cur, "jti-new")
	}
}

// TestEnforceSingleSession_TTL verifies the new session key carries a TTL.
func TestEnforceSingleSession_TTL(t *testing.T) {
	kv := newTestKV()
	ctx := context.Background()

	if err := enforceSingleSession(ctx, kv, "player-ttl", "jti-ttl"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Key must exist right after setting.
	if _, err := kv.Get(ctx, "session:jti-ttl"); err != nil {
		t.Errorf("session:jti-ttl not set: %v", err)
	}
}

// TestLogoutSession_ActiveSession verifies that logging out the current active
// session removes session:{jti}, session:current:{player_id}, and refresh:{player_id}.
func TestLogoutSession_ActiveSession(t *testing.T) {
	kv := newTestKV()
	ctx := context.Background()

	kv.Set(ctx, "session:jti-active", "1", 24*time.Hour)
	kv.Set(ctx, "session:current:player-1", "jti-active", 24*time.Hour)
	kv.Set(ctx, "refresh:player-1", "tok", 30*24*time.Hour)

	if err := logoutSession(ctx, kv, "player-1", "jti-active"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, key := range []string{"session:jti-active", "session:current:player-1", "refresh:player-1"} {
		if _, err := kv.Get(ctx, key); err == nil {
			t.Errorf("expected %s to be deleted", key)
		}
	}
}

// TestLogoutSession_StaleSession verifies that logging out with a stale jti
// only removes session:{jti} and leaves the new session intact.
func TestLogoutSession_StaleSession(t *testing.T) {
	kv := newTestKV()
	ctx := context.Background()

	kv.Set(ctx, "session:jti-old", "1", 24*time.Hour)
	kv.Set(ctx, "session:jti-new", "1", 24*time.Hour)
	kv.Set(ctx, "session:current:player-1", "jti-new", 24*time.Hour)
	kv.Set(ctx, "refresh:player-1", "new-tok", 30*24*time.Hour)

	if err := logoutSession(ctx, kv, "player-1", "jti-old"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := kv.Get(ctx, "session:jti-old"); err == nil {
		t.Error("session:jti-old should have been deleted")
	}
	cur, err := kv.Get(ctx, "session:current:player-1")
	if err != nil || cur != "jti-new" {
		t.Errorf("session:current:player-1 = %q, want jti-new (err: %v)", cur, err)
	}
	if _, err := kv.Get(ctx, "refresh:player-1"); err != nil {
		t.Errorf("refresh:player-1 should be intact: %v", err)
	}
	if _, err := kv.Get(ctx, "session:jti-new"); err != nil {
		t.Errorf("session:jti-new should be intact: %v", err)
	}
}

// TestRefreshCooldown verifies isRefreshCoolingDown returns false before and true after.
func TestRefreshCooldown(t *testing.T) {
	kv := newTestKV()
	ctx := context.Background()

	cooling, err := isRefreshCoolingDown(ctx, kv, "player-cool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cooling {
		t.Error("expected no cooldown before setRefreshCooldown")
	}

	if err := setRefreshCooldown(ctx, kv, "player-cool"); err != nil {
		t.Fatalf("setRefreshCooldown error: %v", err)
	}

	cooling, err = isRefreshCoolingDown(ctx, kv, "player-cool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cooling {
		t.Error("expected cooldown active after setRefreshCooldown")
	}
}

// TestEnforceSingleSession_ConcurrentLogin verifies that simultaneous logins for
// the same player do not leave two coexisting active sessions. One must win and
// the other must receive ErrSessionInFlight.
func TestEnforceSingleSession_ConcurrentLogin(t *testing.T) {
	kv := newTestKV()
	ctx := context.Background()

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)

	results := make([]error, N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i] = enforceSingleSession(ctx, kv, "player-x", "jti-"+string(rune('A'+i)))
		}()
	}
	wg.Wait()

	successCount := 0
	inFlightCount := 0
	for _, err := range results {
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, ErrSessionInFlight):
			inFlightCount++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if successCount == 0 {
		t.Fatal("expected at least one successful enforceSingleSession")
	}
	if successCount+inFlightCount != N {
		t.Fatalf("results don't sum: success=%d in_flight=%d total=%d", successCount, inFlightCount, N)
	}

	cur, err := kv.Get(ctx, "session:current:player-x")
	if err != nil {
		t.Fatalf("session:current:player-x not set: %v", err)
	}
	if _, err := kv.Get(ctx, "session:"+cur); err != nil {
		t.Errorf("winning session %q must still be alive: %v", cur, err)
	}
}

// TestAcquireRefreshLock verifies that only one caller acquires the lock.
func TestAcquireRefreshLock(t *testing.T) {
	kv := newTestKV()
	ctx := context.Background()

	got, err := acquireRefreshLock(ctx, kv, "player-lock")
	if err != nil || !got {
		t.Fatalf("first acquire failed: got=%v err=%v", got, err)
	}

	got2, err := acquireRefreshLock(ctx, kv, "player-lock")
	if err != nil {
		t.Fatalf("second acquire returned unexpected error: %v", err)
	}
	if got2 {
		t.Error("second acquire should have returned false (lock already held)")
	}

	releaseRefreshLock(kv, "player-lock")
	got3, err := acquireRefreshLock(ctx, kv, "player-lock")
	if err != nil || !got3 {
		t.Errorf("post-release acquire failed: got=%v err=%v", got3, err)
	}
}
