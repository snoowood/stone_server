package gacha

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gensdeis/stone-server/internal/cairn"
	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/sqlitedb"
	"github.com/gensdeis/stone-server/pkg/store"
)

// newSQLiteHandler builds a gacha Handler backed by a real SQLite DB so the cairn
// completion CAS + layer_count reset can be exercised end-to-end (the mock-based tests
// only cover the transaction orchestration).
func newSQLiteHandler(t *testing.T) (*Handler, store.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gacha.db")
	raw, err := sqlitedb.New(path, cairn.Default.SlotCount, int(cairn.Default.PhaseOffset().Seconds()))
	if err != nil {
		t.Fatalf("sqlitedb.New: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	db := store.NewSQLAdapter(raw)
	return NewHandler(db, kvstore.NewMemStore(), newTestPool(t), DefaultConfig, cairn.Default), db
}

// seedCairnPull seeds an affordable player with cairn_last_growth_at=now (steps=0, so the
// in-tx ApplyGrowth is a no-op and pull sees exactly the seeded layer_count) plus one
// cairn slot at the given layer_count. next_gacha_at is left NULL (no cooldown).
func seedCairnPull(t *testing.T, db store.DB, playerID string, slotIndex, layerCount int) {
	t.Helper()
	ctx := context.Background()
	nowStr := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	if _, err := db.Exec(ctx, `INSERT INTO players (id, steam_id) VALUES (?, ?)`, playerID, "steam-"+playerID); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO player_states (player_id, enlightenment_pts, enlightenment_rate, last_sync_at, cairn_last_growth_at)
		 VALUES (?, 10000, 1.0, ?, ?)`,
		playerID, nowStr, nowStr); err != nil {
		t.Fatalf("seed player_state: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO wish_cairn_slots (player_id, slot_index, started_at, layer_count) VALUES (?, ?, ?, ?)`,
		playerID, slotIndex, nowStr, layerCount); err != nil {
		t.Fatalf("seed slot: %v", err)
	}
}

func readSlotLayer(t *testing.T, db store.DB, playerID string, slotIndex int) int {
	t.Helper()
	var layer int
	if err := db.QueryRow(context.Background(),
		`SELECT layer_count FROM wish_cairn_slots WHERE player_id = ? AND slot_index = ?`,
		playerID, slotIndex).Scan(&layer); err != nil {
		t.Fatalf("read layer_count: %v", err)
	}
	return layer
}

// issue #34: 완성 슬롯(layer_count = MaxLayers) pull 성공 + 해당 슬롯 layer_count=0 리셋.
func TestPull_CompleteSlot_SucceedsAndResetsLayerCount(t *testing.T) {
	h, db := newSQLiteHandler(t)
	seedCairnPull(t, db, "p1", 0, cairn.Default.MaxLayers) // slot 0 complete

	w := callPullWithSlot(h, "p1", 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := readSlotLayer(t, db, "p1", 0); got != 0 {
		t.Errorf("completed slot must reset to layer_count 0, got %d", got)
	}
}

// issue #34: 미완성 슬롯(layer_count < MaxLayers) pull → 403 CAIRN_INCOMPLETE, 리셋 없음.
func TestPull_IncompleteSlot_ForbiddenAndUntouched(t *testing.T) {
	h, db := newSQLiteHandler(t)
	seedCairnPull(t, db, "p1", 0, cairn.Default.MaxLayers-1) // slot 0 one layer short

	w := callPullWithSlot(h, "p1", 0)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "CAIRN_INCOMPLETE") {
		t.Errorf("want CAIRN_INCOMPLETE: %s", w.Body.String())
	}
	if got := readSlotLayer(t, db, "p1", 0); got != cairn.Default.MaxLayers-1 {
		t.Errorf("incomplete slot must be untouched, got layer_count %d", got)
	}
}
