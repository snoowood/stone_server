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
