package auth

import (
	"context"
	"testing"

	"github.com/gensdeis/stone-server/internal/cairn"
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

// ECON-3: a re-login (existing player_states row) must reset last_sync_at = now so
// the closed-app offline window is NOT accrued, while leaving enlightenment_pts
// untouched — the gap is dropped, not credited. Online idle accrual is handled
// separately by the in-session /player/sync loop. initPlayerState is the shared
// session-start hook for AuthSteam/AuthDev/DevToken; /auth/refresh never calls it.
func TestInitPlayerState_ReLoginResetsAnchorWithoutCrediting(t *testing.T) {
	db, _ := newTestDB(t)
	ctx := context.Background()
	playerID := seedPlayer(t, db)

	// Simulate an account that went offline: last_sync_at 1h in the past with a known
	// balance. The next session start must drop that 1h gap, not credit it.
	const balance = 100.0
	if _, err := db.Exec(ctx,
		`UPDATE player_states
		    SET last_sync_at      = strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-1 hour'),
		        enlightenment_pts = ?
		  WHERE player_id = ?`,
		balance, playerID,
	); err != nil {
		t.Fatalf("seed offline gap: %v", err)
	}

	// Re-login routes through initPlayerState again (existing row → ON CONFLICT path).
	if err := initPlayerState(ctx, db, cairn.Default, playerID); err != nil {
		t.Fatalf("initPlayerState (re-login): %v", err)
	}

	var elapsedSec, pts float64
	if err := db.QueryRow(ctx,
		`SELECT CAST(strftime('%s','now') AS REAL) - CAST(strftime('%s', last_sync_at) AS REAL),
		        enlightenment_pts
		   FROM player_states WHERE player_id = ?`,
		playerID,
	).Scan(&elapsedSec, &pts); err != nil {
		t.Fatalf("read state after re-login: %v", err)
	}

	if elapsedSec > 5 {
		t.Fatalf("last_sync_at not reset: %.0fs elapsed since reset, want ~0 (offline gap must be dropped)", elapsedSec)
	}
	if pts != balance {
		t.Fatalf("enlightenment_pts changed on re-login: got %.2f, want %.2f (offline gap must not be credited)", pts, balance)
	}
}
