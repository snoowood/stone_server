package vow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/gensdeis/stone-server/internal/cairn"
	"github.com/gensdeis/stone-server/internal/gacha"
	"github.com/gensdeis/stone-server/pkg/gameconfig"
	"github.com/gensdeis/stone-server/pkg/sqlitedb"
	"github.com/gensdeis/stone-server/pkg/store"
)

func init() { gin.SetMode(gin.TestMode) }

const skinFixture = `skinId,partType,rarity
c_acc1,Accessory,Common
c_top1,Top,Common
u_acc1,Accessory,Uncommon
r_acc1,Accessory,Rare
q_acc1,Accessory,Unique
l_acc1,Accessory,Legendary
`

func testTiers() []gameconfig.VowTier {
	return []gameconfig.VowTier{
		{TargetRarity: "uncommon", MinNPower: 100, MaxNPower: 28800, MinSuccessRate: 5, MaxSuccessRate: 100},
		{TargetRarity: "rare", MinNPower: 500, MaxNPower: 201600, MinSuccessRate: 5, MaxSuccessRate: 90},
		{TargetRarity: "unique", MinNPower: 2500, MaxNPower: 777600, MinSuccessRate: 5, MaxSuccessRate: 80},
		{TargetRarity: "legendary", MinNPower: 10000, MaxNPower: 2592000, MinSuccessRate: 1, MaxSuccessRate: 50},
	}
}

func newTestHandler(t *testing.T, allowDebug bool) (*Handler, store.DB) {
	t.Helper()
	dir := t.TempDir()
	raw, err := sqlitedb.New(filepath.Join(dir, "vow.db"), cairn.Default.SlotCount, int(cairn.Default.PhaseOffset().Seconds()))
	if err != nil {
		t.Fatalf("sqlitedb.New: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	db := store.NewSQLAdapter(raw)

	csvPath := filepath.Join(dir, "skins.csv")
	if err := os.WriteFile(csvPath, []byte(skinFixture), 0o644); err != nil {
		t.Fatalf("write skins.csv: %v", err)
	}
	pool, err := gacha.LoadSkinPool(csvPath)
	if err != nil {
		t.Fatalf("LoadSkinPool: %v", err)
	}
	return NewHandler(db, pool, 7, testTiers(), allowDebug), db
}

func seedPlayer(t *testing.T, db store.DB, playerID string, pts float64) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO players (id, steam_id) VALUES (?, ?)`, playerID, "steam-"+playerID); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO player_states (player_id, enlightenment_pts, enlightenment_rate, last_sync_at) VALUES (?, ?, ?, ?)`,
		playerID, pts, 0.0, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed player_state: %v", err)
	}
}

func seedInventory(t *testing.T, db store.DB, playerID, itemID, rarity string, count int) {
	t.Helper()
	if _, err := db.Exec(context.Background(),
		`INSERT INTO inventories (id, player_id, item_id, rarity, count, item_type, source_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		itemID+"-"+playerID, playerID, itemID, rarity, count, "stone_skin", "debug"); err != nil {
		t.Fatalf("seed inventory: %v", err)
	}
}

func callPray(h *Handler, playerID, body string) (*httptest.ResponseRecorder, prayResponse) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("player_id", playerID)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/vow/pray", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Pray(c)
	var resp prayResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

// 성공: n_power=maxNPower → success_rate 100 → 결정적 성공. target rarity 보상.
func TestPray_Success_TargetRarity(t *testing.T) {
	h, db := newTestHandler(t, false)
	seedPlayer(t, db, "p1", 1_000_000)
	seedInventory(t, db, "p1", "c_acc1", "common", 7)

	w, resp := callPray(h, "p1", `{"materials":[{"item_id":"c_acc1","count":7}],"n_power":28800}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.Result != "Success" {
		t.Errorf("rate 100 → want Success, got %q", resp.Result)
	}
	if resp.BaseRarity != "common" || resp.TargetRarity != "uncommon" {
		t.Errorf("want base=common target=uncommon, got base=%q target=%q", resp.BaseRarity, resp.TargetRarity)
	}
	if resp.RewardRarity != "uncommon" {
		t.Errorf("success → reward should be target uncommon, got %q", resp.RewardRarity)
	}
	if resp.RewardItemID != "u_acc1" {
		t.Errorf("only uncommon skin is u_acc1, got %q", resp.RewardItemID)
	}
	if resp.SourceType != "vow_reward" || resp.Reward.SourceType != "vow_reward" {
		t.Errorf("want source_type vow_reward, got %q / %q", resp.SourceType, resp.Reward.SourceType)
	}
	if resp.Reward.ItemType != "stone_skin" {
		t.Errorf("want reward.item_type stone_skin, got %q", resp.Reward.ItemType)
	}
	if resp.SuccessRate != 100 {
		t.Errorf("want success_rate 100, got %v", resp.SuccessRate)
	}
	if resp.BalanceAfter >= 1_000_000 {
		t.Errorf("n_power should be deducted, balance=%v", resp.BalanceAfter)
	}
	if !resp.IsNewReward {
		t.Errorf("first uncommon → IsNewReward true")
	}
	var cnt int
	if err := db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM inventories WHERE player_id = ? AND item_id = ?`, "p1", "c_acc1").Scan(&cnt); err != nil {
		t.Fatalf("count materials: %v", err)
	}
	if cnt != 0 {
		t.Errorf("materials should be fully consumed (row removed), got %d rows", cnt)
	}
}

// debug force_failure (allowDebug) → base rarity 보상.
func TestPray_ForceFailure_BaseRarity(t *testing.T) {
	h, db := newTestHandler(t, true)
	seedPlayer(t, db, "p1", 1_000_000)
	seedInventory(t, db, "p1", "c_acc1", "common", 7)

	w, resp := callPray(h, "p1",
		`{"materials":[{"item_id":"c_acc1","count":7}],"n_power":28800,"debug_options":{"force_failure":true}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.Result != "Failed" {
		t.Errorf("force_failure → Failed, got %q", resp.Result)
	}
	if resp.RewardRarity != "common" {
		t.Errorf("failure → base common, got %q", resp.RewardRarity)
	}
}

// debug force flags 는 allowDebug=false(production)에선 무시 → RNG 경로.
func TestPray_DebugIgnoredInProd(t *testing.T) {
	h, db := newTestHandler(t, false)
	seedPlayer(t, db, "p1", 1_000_000)
	seedInventory(t, db, "p1", "c_acc1", "common", 7)

	// force_failure 를 줘도 prod 에선 무시되고 rate 100(=n_power max) → 항상 성공.
	w, resp := callPray(h, "p1",
		`{"materials":[{"item_id":"c_acc1","count":7}],"n_power":28800,"debug_options":{"force_failure":true}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.Result != "Success" {
		t.Errorf("prod ignores force_failure; rate 100 → Success, got %q", resp.Result)
	}
}

func TestPray_WrongMaterialCount_400(t *testing.T) {
	h, db := newTestHandler(t, false)
	seedPlayer(t, db, "p1", 1_000_000)
	seedInventory(t, db, "p1", "c_acc1", "common", 5)

	w, _ := callPray(h, "p1", `{"materials":[{"item_id":"c_acc1","count":5}],"n_power":28800}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "INVALID_REQUEST") {
		t.Errorf("want INVALID_REQUEST: %s", w.Body.String())
	}
}

func TestPray_NPowerOutOfRange_400(t *testing.T) {
	h, db := newTestHandler(t, false)
	seedPlayer(t, db, "p1", 1_000_000)
	seedInventory(t, db, "p1", "c_acc1", "common", 7)

	w, _ := callPray(h, "p1", `{"materials":[{"item_id":"c_acc1","count":7}],"n_power":50}`) // < min 100
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPray_LegendaryMaterial_400(t *testing.T) {
	h, db := newTestHandler(t, false)
	seedPlayer(t, db, "p1", 1_000_000)
	seedInventory(t, db, "p1", "l_acc1", "legendary", 7)

	w, _ := callPray(h, "p1", `{"materials":[{"item_id":"l_acc1","count":7}],"n_power":28800}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("legendary material not craftable → want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPray_InsufficientPoints_409(t *testing.T) {
	h, db := newTestHandler(t, false)
	seedPlayer(t, db, "p1", 100) // < n_power 28800
	seedInventory(t, db, "p1", "c_acc1", "common", 7)

	w, _ := callPray(h, "p1", `{"materials":[{"item_id":"c_acc1","count":7}],"n_power":28800}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "INSUFFICIENT_POINTS") {
		t.Errorf("want INSUFFICIENT_POINTS: %s", w.Body.String())
	}
}

func TestPray_InsufficientMaterials_409(t *testing.T) {
	h, db := newTestHandler(t, false)
	seedPlayer(t, db, "p1", 1_000_000)
	seedInventory(t, db, "p1", "c_acc1", "common", 3) // 선언은 7, 보유는 3

	w, _ := callPray(h, "p1", `{"materials":[{"item_id":"c_acc1","count":7}],"n_power":28800}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "INSUFFICIENT_MATERIALS") {
		t.Errorf("want INSUFFICIENT_MATERIALS: %s", w.Body.String())
	}
	// rollback 검증: 잔고 차감 없어야 함.
	var bal float64
	if err := db.QueryRow(context.Background(),
		`SELECT enlightenment_pts FROM player_states WHERE player_id = ?`, "p1").Scan(&bal); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if bal < 1_000_000 {
		t.Errorf("material 실패 시 잔고 rollback 되어야 함, got %v", bal)
	}
}

// base/target 산식 parity (클라 TryComputeBaseAndTargetRarity).
func TestComputeRarities(t *testing.T) {
	h, _ := newTestHandler(t, false)

	base, target, ok := h.computeRarities([]material{{ItemID: "c_acc1", Count: 7}})
	if !ok || base != gacha.Common || target != gacha.Uncommon {
		t.Errorf("common×7 → want base=common target=uncommon, got base=%q target=%q ok=%v", base, target, ok)
	}
	// avg = (1×1 + 3×1)/2 = 2.0 → base uncommon, target rare
	base, target, ok = h.computeRarities([]material{{ItemID: "c_acc1", Count: 1}, {ItemID: "r_acc1", Count: 1}})
	if !ok || base != gacha.Uncommon || target != gacha.Rare {
		t.Errorf("avg2 → want base=uncommon target=rare, got base=%q target=%q ok=%v", base, target, ok)
	}
	if _, _, ok := h.computeRarities([]material{{ItemID: "l_acc1", Count: 1}}); ok {
		t.Error("legendary material should be rejected")
	}
	if _, _, ok := h.computeRarities([]material{{ItemID: "nope", Count: 1}}); ok {
		t.Error("unknown material should be rejected")
	}
}

// success_rate parity (클라 CalculateSuccessRate: InverseLerp→Lerp→Clamp).
func TestCalcSuccessRate_Parity(t *testing.T) {
	tier := gameconfig.VowTier{TargetRarity: "uncommon", MinNPower: 100, MaxNPower: 28800, MinSuccessRate: 5, MaxSuccessRate: 100}
	if got := calcSuccessRate(tier, 100); got != 5 {
		t.Errorf("at minNPower want 5, got %v", got)
	}
	if got := calcSuccessRate(tier, 28800); got != 100 {
		t.Errorf("at maxNPower want 100, got %v", got)
	}
	if got := calcSuccessRate(tier, 0); got != 5 {
		t.Errorf("below min clamps to 5, got %v", got)
	}
	if got := calcSuccessRate(tier, 9_999_999); got != 100 {
		t.Errorf("above max clamps to 100, got %v", got)
	}
	if mid := calcSuccessRate(tier, (100+28800)/2.0); mid <= 5 || mid >= 100 {
		t.Errorf("midpoint should be strictly between 5 and 100, got %v", mid)
	}
	deg := gameconfig.VowTier{MinNPower: 100, MaxNPower: 100, MinSuccessRate: 5, MaxSuccessRate: 42}
	if got := calcSuccessRate(deg, 100); got != 42 {
		t.Errorf("degenerate (max<=min) → maxSuccessRate 42, got %v", got)
	}
}
