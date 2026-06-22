package auth

import (
	"context"
	"testing"
)

// E2E-3: a freshly created player_states row must have last_sync_at anchored
// (non-NULL) so passive accrual starts at t0 instead of yielding 0 and 409ing the
// first gacha. seedPlayer runs the real initPlayerState path.
func TestInitPlayerState_AnchorsLastSyncAt(t *testing.T) {
	db, _ := newTestDB(t)
	playerID := seedPlayer(t, db)

	var nonNull int
	if err := db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM player_states WHERE player_id = ? AND last_sync_at IS NOT NULL`,
		playerID,
	).Scan(&nonNull); err != nil {
		t.Fatalf("query last_sync_at: %v", err)
	}
	if nonNull != 1 {
		t.Fatalf("last_sync_at must be anchored (non-NULL) on init, got NULL")
	}
}
