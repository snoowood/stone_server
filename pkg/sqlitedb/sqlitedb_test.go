package sqlitedb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func countSlots(t *testing.T, db *sql.DB, playerID string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM wish_cairn_slots WHERE player_id = ?", playerID,
	).Scan(&n); err != nil {
		t.Fatalf("count slots: %v", err)
	}
	return n
}

// TestBackfillCairnSlots_GrowAndShrink verifies the WishCairn backfill tracks the
// configured slotCount across reopens: backfill creates exactly slotCount slots for
// existing players, shrinking prunes extras, growing re-adds the missing indices.
func TestBackfillCairnSlots_GrowAndShrink(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cairn.db")

	// Fresh DB (no players) → backfill is a no-op.
	db, err := New(path, 5, 30)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO players (id, steam_id) VALUES ('p1', 's1')`); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	db.Close()

	// Reopen with slotCount=5 → existing player p1 backfilled to 5 slots.
	db, err = New(path, 5, 30)
	if err != nil {
		t.Fatalf("New (backfill): %v", err)
	}
	if n := countSlots(t, db, "p1"); n != 5 {
		t.Errorf("after backfill: want 5 slots, got %d", n)
	}
	db.Close()

	// Reopen with slotCount=3 → shrink: slot_index >= 3 pruned.
	db, err = New(path, 3, 50)
	if err != nil {
		t.Fatalf("New (shrink): %v", err)
	}
	if n := countSlots(t, db, "p1"); n != 3 {
		t.Errorf("after shrink: want 3 slots, got %d", n)
	}
	db.Close()

	// Reopen with slotCount=5 → grow: missing indices 3,4 re-added.
	db, err = New(path, 5, 30)
	if err != nil {
		t.Fatalf("New (grow): %v", err)
	}
	if n := countSlots(t, db, "p1"); n != 5 {
		t.Errorf("after grow: want 5 slots, got %d", n)
	}
	db.Close()
}

// TestNew_MirrorsMigration0009OnReusedDB simulates a reused DB whose player_states
// predates migrations/000009 (which added enlightenment_rate + last_sync_at, never
// mirrored here). New must add BOTH columns and backfill last_sync_at to non-NULL
// without failing on "no such column" — guarding the E2E-3 first-gacha-409 fix and the
// accrual queries (which reference enlightenment_rate) on legacy SQLite DBs.
func TestNew_MirrorsMigration0009OnReusedDB(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := raw.ExecContext(ctx,
		`CREATE TABLE player_states (player_id TEXT PRIMARY KEY, enlightenment_pts REAL NOT NULL DEFAULT 0)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO player_states (player_id) VALUES ('p1')`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	raw.Close()

	db, err := New(path, 5, 30)
	if err != nil {
		t.Fatalf("New on reused legacy DB (column-add + backfill must not fail): %v", err)
	}
	defer db.Close()

	var (
		lastSyncNonNull int
		rate            float64
	)
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(enlightenment_rate) FROM player_states WHERE player_id = 'p1' AND last_sync_at IS NOT NULL`,
	).Scan(&lastSyncNonNull, &rate); err != nil {
		t.Fatalf("query player_states: %v", err)
	}
	if lastSyncNonNull != 1 {
		t.Errorf("reused-DB row must have last_sync_at backfilled to non-NULL")
	}
	if rate != 1.0 {
		t.Errorf("reused-DB row must have enlightenment_rate defaulted to 1.0, got %v", rate)
	}
}

// startedAts returns each slot's started_at ordered by slot_index.
func startedAts(t *testing.T, db *sql.DB, playerID string) []time.Time {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		"SELECT started_at FROM wish_cairn_slots WHERE player_id = ? ORDER BY slot_index ASC", playerID)
	if err != nil {
		t.Fatalf("query started_at: %v", err)
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("parse started_at %q: %v", s, err)
		}
		out = append(out, ts)
	}
	return out
}

// TestBackfillCairnSlots_StaggerSpacing verifies newly created slots use the
// injected phaseOffsetSeconds for their stagger (slot k = now - k*phaseOffset).
func TestBackfillCairnSlots_StaggerSpacing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stagger.db")
	const phase = 50

	db, err := New(path, 3, phase)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO players (id, steam_id) VALUES ('p1', 's1')`); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	db.Close()

	db, err = New(path, 3, phase) // backfill creates all 3 slots fresh
	if err != nil {
		t.Fatalf("New (backfill): %v", err)
	}
	defer db.Close()

	ts := startedAts(t, db, "p1")
	if len(ts) != 3 {
		t.Fatalf("want 3 slots, got %d", len(ts))
	}
	// slot k should sit k*phase seconds before slot 0, within a small clock tolerance.
	for k := 1; k < len(ts); k++ {
		gap := ts[0].Sub(ts[k]).Seconds()
		want := float64(k * phase)
		if d := gap - want; d < -2 || d > 2 {
			t.Errorf("slot %d gap from slot 0 = %.0fs, want ~%.0fs", k, gap, want)
		}
	}
}
