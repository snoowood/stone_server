package auth

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/gensdeis/stone-server/internal/cairn"
	"github.com/gensdeis/stone-server/pkg/sqlitedb"
	"github.com/gensdeis/stone-server/pkg/store"
)

// newTestDB spins up an in-memory-like SQLite at a temp path with the schema applied.
func newTestDB(t *testing.T) (store.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "streak.db")
	raw, err := sqlitedb.New(path, cairn.Default.SlotCount, int(cairn.Default.PhaseOffset().Seconds()))
	if err != nil {
		t.Fatalf("sqlitedb.New: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	return store.NewSQLAdapter(raw), path
}

func seedPlayer(t *testing.T, db store.DB) string {
	t.Helper()
	ctx := context.Background()
	playerID := uuid.New().String()

	if _, err := db.Exec(ctx,
		`INSERT INTO players (id, steam_id) VALUES (?, ?)`,
		playerID, "steam-"+playerID,
	); err != nil {
		t.Fatalf("seed players: %v", err)
	}
	if err := initPlayerState(ctx, db, cairn.Default, playerID); err != nil {
		t.Fatalf("initPlayerState: %v", err)
	}
	return playerID
}

func setLastLogin(t *testing.T, db store.DB, playerID, dateModifier string) {
	t.Helper()
	q := `UPDATE player_states SET last_login_date = date('now', '` + dateModifier + `'), streak_days = ? WHERE player_id = ?`
	if _, err := db.Exec(context.Background(), q, 5, playerID); err != nil {
		t.Fatalf("setLastLogin: %v", err)
	}
}

func readStreak(t *testing.T, db store.DB, playerID string) int {
	t.Helper()
	var s int
	if err := db.QueryRow(context.Background(),
		`SELECT streak_days FROM player_states WHERE player_id = ?`, playerID,
	).Scan(&s); err != nil {
		t.Fatalf("read streak: %v", err)
	}
	return s
}

func TestUpdateLoginStreak_FirstLoginSetsToOne(t *testing.T) {
	db, _ := newTestDB(t)
	playerID := seedPlayer(t, db)

	if err := updateLoginStreak(context.Background(), db, playerID); err != nil {
		t.Fatalf("updateLoginStreak: %v", err)
	}
	if got := readStreak(t, db, playerID); got != 1 {
		t.Fatalf("first login streak = %d, want 1", got)
	}
}

func TestUpdateLoginStreak_SameDayUnchanged(t *testing.T) {
	db, _ := newTestDB(t)
	playerID := seedPlayer(t, db)
	setLastLogin(t, db, playerID, "+0 day") // today

	if err := updateLoginStreak(context.Background(), db, playerID); err != nil {
		t.Fatalf("updateLoginStreak: %v", err)
	}
	if got := readStreak(t, db, playerID); got != 5 {
		t.Fatalf("same-day streak = %d, want 5 (unchanged)", got)
	}
}

func TestUpdateLoginStreak_PreviousDayIncrements(t *testing.T) {
	db, _ := newTestDB(t)
	playerID := seedPlayer(t, db)
	setLastLogin(t, db, playerID, "-1 day") // yesterday

	if err := updateLoginStreak(context.Background(), db, playerID); err != nil {
		t.Fatalf("updateLoginStreak: %v", err)
	}
	if got := readStreak(t, db, playerID); got != 6 {
		t.Fatalf("yesterday-streak = %d, want 6", got)
	}
}

func TestUpdateLoginStreak_GapResetsToOne(t *testing.T) {
	db, _ := newTestDB(t)
	playerID := seedPlayer(t, db)
	setLastLogin(t, db, playerID, "-3 days") // 3-day gap

	if err := updateLoginStreak(context.Background(), db, playerID); err != nil {
		t.Fatalf("updateLoginStreak: %v", err)
	}
	if got := readStreak(t, db, playerID); got != 1 {
		t.Fatalf("3-day gap streak = %d, want 1 (reset)", got)
	}
}
