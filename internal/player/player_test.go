package player

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/gensdeis/stone-server/internal/cairn"
	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/sqlitedb"
	"github.com/gensdeis/stone-server/pkg/store"
)

func init() { gin.SetMode(gin.TestMode) }

func newTestHandler(t *testing.T) (*Handler, store.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "player.db")
	raw, err := sqlitedb.New(path, cairn.Default.SlotCount, int(cairn.Default.PhaseOffset().Seconds()))
	if err != nil {
		t.Fatalf("sqlitedb.New: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	db := store.NewSQLAdapter(raw)
	return NewHandler(db, kvstore.NewMemStore(), cairn.Default), db
}

// seedPlayerState inserts a player + player_state with a given balance, rate, and
// last_sync_at (stored as RFC3339, matching the server's write format).
func seedPlayerState(t *testing.T, db store.DB, playerID string, pts, rate float64, lastSyncAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO players (id, steam_id) VALUES (?, ?)`,
		playerID, "steam-"+playerID); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO player_states (player_id, enlightenment_pts, enlightenment_rate, last_sync_at)
		 VALUES (?, ?, ?, ?)`,
		playerID, pts, rate, lastSyncAt.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed player_state: %v", err)
	}
}

func callSync(h *Handler, playerID string) (*httptest.ResponseRecorder, syncResponse) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("player_id", playerID)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/player/sync", nil)
	h.Sync(c)
	var resp syncResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

func callGetState(h *Handler, playerID string) (*httptest.ResponseRecorder, stateResponse) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("player_id", playerID)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/player/state", nil)
	h.GetState(c)
	var resp stateResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

// TestSync_AccruesElapsedTimesRate: passive gain = elapsed × rate added to balance.
func TestSync_AccruesElapsedTimesRate(t *testing.T) {
	h, db := newTestHandler(t)
	const initial, rate, elapsed = 100.0, 2.0, 100.0
	seedPlayerState(t, db, "p1", initial, rate, time.Now().Add(-time.Duration(elapsed)*time.Second))

	w, resp := callSync(h, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	// elapsed ≈ 100s (±a few seconds of test execution) × rate 2.0 → ~200 gained.
	gained := resp.EnlightenmentPts - initial
	wantMin, wantMax := (elapsed-2)*rate, (elapsed+5)*rate
	if gained < wantMin || gained > wantMax {
		t.Errorf("accrual %.2f out of expected band [%.2f, %.2f]", gained, wantMin, wantMax)
	}
	if resp.LastSyncAt == nil {
		t.Error("last_sync_at should be returned (advanced to now)")
	}
}

// TestSync_ClockSkew_FutureLastSync_NoDecrease: 미래 last_sync_at (clock skew) 시
// MAX(0, ...) 로 음수 elapsed 가 잔고를 깎지 않는다.
func TestSync_ClockSkew_FutureLastSync_NoDecrease(t *testing.T) {
	h, db := newTestHandler(t)
	const initial, rate = 500.0, 1.0
	seedPlayerState(t, db, "p1", initial, rate, time.Now().Add(1*time.Hour)) // last_sync 1h in the future

	w, resp := callSync(h, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.EnlightenmentPts != initial {
		t.Errorf("future last_sync_at must clamp accrual to 0: want %.2f, got %.2f", initial, resp.EnlightenmentPts)
	}
}

func TestSync_PlayerStateNotFound_404(t *testing.T) {
	h, _ := newTestHandler(t)
	w, _ := callSync(h, "ghost")
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 for missing player state, got %d", w.Code)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "NOT_FOUND" {
		t.Errorf("want error code NOT_FOUND, got %q", body.Code)
	}
}

// TestGetState_ReturnsEnlightenmentRate: rate 는 /player/state 응답으로만 내려가는
// 권위값(클라 anchorRate 주입원). 누락되면 클라 외삽이 잘못된 기울기를 쓴다.
func TestGetState_ReturnsEnlightenmentRate(t *testing.T) {
	h, db := newTestHandler(t)
	const rate = 3.5
	seedPlayerState(t, db, "p1", 10.0, rate, time.Now())

	w, resp := callGetState(h, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.EnlightenmentRate != rate {
		t.Errorf("want enlightenment_rate %.2f, got %.2f", rate, resp.EnlightenmentRate)
	}
}

// DTO sync: 인벤토리 행의 item_type/source_type provenance 가 /player/state 응답에
// 1:1 로 노출되어 클라 InventoryItemDto(ResolveItemType/ParseSourceType) 가 권위값을 받는다.
func TestGetState_InventoryExposesGrantMeta(t *testing.T) {
	h, db := newTestHandler(t)
	seedPlayerState(t, db, "p1", 10.0, 1.0, time.Now())
	ctx := context.Background()
	if _, err := db.Exec(ctx,
		`INSERT INTO inventories (id, player_id, item_id, rarity, count, item_type, source_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"inv1", "p1", "skin_001", "rare", 2, "wish_cairn_skin", "vow_reward"); err != nil {
		t.Fatalf("seed inventory: %v", err)
	}

	w, resp := callGetState(h, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(resp.Inventory) != 1 {
		t.Fatalf("want 1 inventory item, got %d", len(resp.Inventory))
	}
	it := resp.Inventory[0]
	if it.ItemType != "wish_cairn_skin" {
		t.Errorf("want item_type wish_cairn_skin, got %q", it.ItemType)
	}
	if it.SourceType != "vow_reward" {
		t.Errorf("want source_type vow_reward, got %q", it.SourceType)
	}
	if it.Count != 2 {
		t.Errorf("want count 2, got %d", it.Count)
	}
}

// seedCairn seeds SlotCount slots (all layer_count 0) and sets cairn_last_growth_at
// for a player that already has a player_states row.
func seedCairn(t *testing.T, db store.DB, playerID string, anchor time.Time) {
	t.Helper()
	ctx := context.Background()
	nowStr := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	for i := 0; i < cairn.Default.SlotCount; i++ {
		if _, err := db.Exec(ctx,
			`INSERT INTO wish_cairn_slots (player_id, slot_index, started_at, layer_count) VALUES (?, ?, ?, 0)`,
			playerID, i, nowStr); err != nil {
			t.Fatalf("seed slot %d: %v", i, err)
		}
	}
	if _, err := db.Exec(ctx,
		`UPDATE player_states SET cairn_last_growth_at = ? WHERE player_id = ?`,
		anchor.UTC().Truncate(time.Second).Format(time.RFC3339), playerID); err != nil {
		t.Fatalf("seed cairn anchor: %v", err)
	}
}

func cairnLayerSum(t *testing.T, db store.DB, playerID string) int {
	t.Helper()
	var sum int
	if err := db.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(layer_count), 0) FROM wish_cairn_slots WHERE player_id = ?`, playerID).Scan(&sum); err != nil {
		t.Fatalf("sum layers: %v", err)
	}
	return sum
}

// issue #34: GetState 응답이 저장형 성장(anchor→now)을 반영하고, 연속 호출 시 이중 성장이
// 없어야 한다(앵커가 전진했으므로 두 번째 호출은 steps=0).
func TestGetState_AppliesCairnGrowthOnce(t *testing.T) {
	h, db := newTestHandler(t)
	seedPlayerState(t, db, "p1", 10.0, 1.0, time.Now())
	interval := time.Duration(cairn.Default.SpawnIntervalSeconds) * time.Second
	seedCairn(t, db, "p1", time.Now().Add(-3*interval)) // 3 steps pending

	w, resp := callGetState(h, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	got := 0
	for _, s := range resp.Cairn.Slots {
		got += s.LayerCount
	}
	if got != 3 {
		t.Fatalf("GetState should reflect 3 grown layers, got %d (%+v)", got, resp.Cairn.Slots)
	}

	_, resp2 := callGetState(h, "p1")
	got2 := 0
	for _, s := range resp2.Cairn.Slots {
		got2 += s.LayerCount
	}
	if got2 != got {
		t.Errorf("consecutive GetState double-grew: %d → %d", got, got2)
	}
}

// issue #34: Sync 의 성장은 best-effort — 성장 적용이 실패해도(여기선 파싱 불가 앵커)
// 이미 커밋된 적립을 뒤집지 않고 200 + sync 응답을 유지해야 한다(다음 요청에서 재시도).
func TestSync_GrowthFailure_StillReturns200(t *testing.T) {
	h, db := newTestHandler(t)
	seedPlayerState(t, db, "p1", 100.0, 1.0, time.Now())
	seedCairn(t, db, "p1", time.Now())
	if _, err := db.Exec(context.Background(),
		`UPDATE player_states SET cairn_last_growth_at = 'not-a-timestamp' WHERE player_id = 'p1'`); err != nil {
		t.Fatalf("corrupt anchor: %v", err)
	}

	w, resp := callSync(h, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("growth failure must not fail sync (best-effort): want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.EnlightenmentPts < 100.0 {
		t.Errorf("sync response must still carry the committed accrual, got %v", resp.EnlightenmentPts)
	}
}

// issue #34: Sync 도 성장을 적립한다(응답엔 cairn 이 없으므로 DB 로 확인).
func TestSync_AppliesCairnGrowth(t *testing.T) {
	h, db := newTestHandler(t)
	seedPlayerState(t, db, "p1", 10.0, 1.0, time.Now())
	interval := time.Duration(cairn.Default.SpawnIntervalSeconds) * time.Second
	seedCairn(t, db, "p1", time.Now().Add(-2*interval)) // 2 steps pending

	w, _ := callSync(h, "p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := cairnLayerSum(t, db, "p1"); got != 2 {
		t.Errorf("Sync should apply 2 grown layers, got %d", got)
	}
}
