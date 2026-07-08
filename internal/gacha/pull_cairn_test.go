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
// in-tx ApplyGrowth is a no-op and pull sees exactly the seeded layer_count) plus a FULL
// slot set: the target slot at the given layer_count, every other slot complete
// (MaxLayers). 전체 세트인 이유 — ApplyGrowth 는 SlotCount 미만이면 성장을 보류하고,
// 나머지를 완성 상태로 두면 미완성이 타깃 하나뿐이라 성장 배분이 시드와 무관하게
// 결정적이다. next_gacha_at is left NULL (no cooldown).
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
	for i := 0; i < cairn.Default.SlotCount; i++ {
		layer := cairn.Default.MaxLayers
		if i == slotIndex {
			layer = layerCount
		}
		if _, err := db.Exec(ctx,
			`INSERT INTO wish_cairn_slots (player_id, slot_index, started_at, layer_count) VALUES (?, ?, ?, ?)`,
			playerID, i, nowStr, layer); err != nil {
			t.Fatalf("seed slot %d: %v", i, err)
		}
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

// setCairnAnchor backdates cairn_last_growth_at so the in-tx ApplyGrowth sees steps>0.
func setCairnAnchor(t *testing.T, db store.DB, playerID string, ago time.Duration) string {
	t.Helper()
	anchor := time.Now().Add(-ago).UTC().Truncate(time.Second).Format(time.RFC3339)
	if _, err := db.Exec(context.Background(),
		`UPDATE player_states SET cairn_last_growth_at = ? WHERE player_id = ?`, anchor, playerID); err != nil {
		t.Fatalf("set cairn anchor: %v", err)
	}
	return anchor
}

func readCairnAnchor(t *testing.T, db store.DB, playerID string) string {
	t.Helper()
	var a string
	if err := db.QueryRow(context.Background(),
		`SELECT cairn_last_growth_at FROM player_states WHERE player_id = ?`, playerID).Scan(&a); err != nil {
		t.Fatalf("read cairn anchor: %v", err)
	}
	return a
}

// issue #34 회귀 가드: pull tx 내부 성장(steps>0)이 완성판정보다 먼저 실행되어야
// "방금 MaxLayers 에 도달한 슬롯"을 같은 요청에서 즉시 뽑을 수 있다. ApplyGrowth 호출이
// claim CAS 뒤로 밀리거나 제거되면 이 테스트가 403 으로 잡는다. 슬롯이 1개뿐이라
// 미완성 목록이 그 슬롯 하나 → 시드와 무관하게 배분이 결정적이다.
func TestPull_GrowthInsideTx_JustCompletedSlotClaimable(t *testing.T) {
	h, db := newSQLiteHandler(t)
	seedCairnPull(t, db, "p1", 0, cairn.Default.MaxLayers-1) // one layer short
	interval := time.Duration(cairn.Default.SpawnIntervalSeconds) * time.Second
	setCairnAnchor(t, db, "p1", interval+2*time.Second) // steps>=1 → +1층 → 완성

	w := callPullWithSlot(h, "p1", 0)
	if w.Code != http.StatusOK {
		t.Fatalf("in-tx growth must complete the slot before the claim CAS: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := readSlotLayer(t, db, "p1", 0); got != 0 {
		t.Errorf("claimed slot must reset to 0, got %d", got)
	}
}

// issue #34 회귀 가드: 실패한 pull(403 INCOMPLETE)은 tx 롤백으로 성장 증분과 앵커 전진을
// 함께 되돌려야 한다(원자성). 앵커만 전진하고 층이 롤백되면 그 성장 윈도우가 소실되고,
// 반대로 층만 남으면 이중 성장이 된다. 되돌아간 윈도우는 다음 요청에서 동일 시드로
// 재적립되므로 손실도 없다.
func TestPull_IncompleteAfterGrowth_RollsBackGrowthAtomically(t *testing.T) {
	h, db := newSQLiteHandler(t)
	seedCairnPull(t, db, "p1", 0, 0) // far from complete
	interval := time.Duration(cairn.Default.SpawnIntervalSeconds) * time.Second
	seededAnchor := setCairnAnchor(t, db, "p1", interval+2*time.Second) // steps>=1

	w := callPullWithSlot(h, "p1", 0)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	if got := readSlotLayer(t, db, "p1", 0); got != 0 {
		t.Errorf("rolled-back pull must undo the in-tx growth: layer_count %d, want 0", got)
	}
	if got := readCairnAnchor(t, db, "p1"); got != seededAnchor {
		t.Errorf("rolled-back pull must undo the anchor advance: %q, want %q", got, seededAnchor)
	}
}
