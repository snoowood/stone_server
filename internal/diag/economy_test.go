package diag

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
	"github.com/gensdeis/stone-server/pkg/sqlitedb"
	"github.com/gensdeis/stone-server/pkg/store"
)

func init() { gin.SetMode(gin.TestMode) }

func newTestDB(t *testing.T) store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "diag.db")
	raw, err := sqlitedb.New(path, cairn.Default.SlotCount, int(cairn.Default.PhaseOffset().Seconds()))
	if err != nil {
		t.Fatalf("sqlitedb.New: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	return store.NewSQLAdapter(raw)
}

func seedPlayer(t *testing.T, db store.DB, playerID string, pts, rate float64) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO players (id, steam_id) VALUES (?, ?)`,
		playerID, "steam-"+playerID); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO player_states (player_id, enlightenment_pts, enlightenment_rate, last_sync_at)
		 VALUES (?, ?, ?, ?)`,
		playerID, pts, rate, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed player_state: %v", err)
	}
}

func seedGachaLog(t *testing.T, db store.DB, id, playerID string, cost, accrued, before, after float64, at time.Time) {
	t.Helper()
	if _, err := db.Exec(context.Background(),
		`INSERT INTO gacha_logs (id, player_id, item_id, rarity, cost_points, accrued_pts, balance_before, balance_after, pulled_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, playerID, "item-"+id, "common", cost, accrued, before, after, at.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed gacha_log: %v", err)
	}
}

func seedVowLog(t *testing.T, db store.DB, id, playerID string, cost, accrued, before, after float64, at time.Time) {
	t.Helper()
	if _, err := db.Exec(context.Background(),
		`INSERT INTO vow_logs (id, player_id, base_rarity, target_rarity, result, reward_item_id, reward_rarity, cost_points, accrued_pts, balance_before, balance_after, prayed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, playerID, "common", "rare", "success", "item-"+id, "rare", cost, accrued, before, after, at.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed vow_log: %v", err)
	}
}

func callEconomy(db store.DB, query string) (*httptest.ResponseRecorder, economyAudit) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/internal/diag/economy"+query, nil)
	EconomyHandler(db)(c)
	var resp economyAudit
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error body: %v", err)
	}
	return body.Code
}

func TestEconomy_MissingPlayerID_400(t *testing.T) {
	db := newTestDB(t)
	w, _ := callEconomy(db, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if got := errCode(t, w); got != "INVALID_REQUEST" {
		t.Errorf("want code INVALID_REQUEST, got %q", got)
	}
}

func TestEconomy_PlayerNotFound_404(t *testing.T) {
	db := newTestDB(t)
	w, _ := callEconomy(db, "?player_id=ghost")
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
	if got := errCode(t, w); got != "NOT_FOUND" {
		t.Errorf("want code NOT_FOUND, got %q", got)
	}
}

func TestEconomy_NoEvents_Reconciled(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 100, 1.0)

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !resp.Reconciled {
		t.Errorf("want reconciled=true for a player with no events")
	}
	if resp.TotalEvents != 0 {
		t.Errorf("want 0 events, got %d", resp.TotalEvents)
	}
	if resp.CurrentBalance != 100 {
		t.Errorf("want current_balance 100, got %v", resp.CurrentBalance)
	}
	if len(resp.Anomalies) != 0 {
		t.Errorf("want no anomalies, got %+v", resp.Anomalies)
	}
	if resp.FirstBalanceBefore != nil || resp.LastBalanceAfter != nil {
		t.Errorf("want nil window bounds with no events")
	}
}

// A consistent ledger with one un-logged passive-accrual gap (banked via /player/sync)
// must reconcile, and the gap must surface as unlogged_accrual rather than an anomaly.
func TestEconomy_HealthyChain_Reconciled(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 35, 1.0)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedGachaLog(t, db, "g1", "p1", 30, 0, 100, 70, t0)                   // 100 + 0 - 30 = 70
	seedVowLog(t, db, "v1", "p1", 20, 0, 80, 60, t0.Add(time.Minute))     // gap 80-70 = +10 (sync), within 60s ceiling
	seedGachaLog(t, db, "g2", "p1", 30, 5, 60, 35, t0.Add(2*time.Minute)) // gap 60-60 = 0

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !resp.Reconciled {
		t.Fatalf("want reconciled, got anomalies %+v", resp.Anomalies)
	}
	if resp.TotalEvents != 3 || resp.GachaPulls != 2 || resp.VowCount != 1 {
		t.Errorf("counts: events=%d gacha=%d vow=%d", resp.TotalEvents, resp.GachaPulls, resp.VowCount)
	}
	if resp.TotalAccrued != 5 {
		t.Errorf("want total_accrued 5, got %v", resp.TotalAccrued)
	}
	if resp.TotalCost != 80 {
		t.Errorf("want total_cost 80, got %v", resp.TotalCost)
	}
	if resp.NetLogged != -75 {
		t.Errorf("want net_logged -75, got %v", resp.NetLogged)
	}
	// Only the +10 gap is banked accrual; the 0 gap contributes nothing.
	if resp.UnloggedAccrual != 10 {
		t.Errorf("want unlogged_accrual 10, got %v", resp.UnloggedAccrual)
	}
	if resp.SameSecondEvents {
		t.Errorf("want same_second_events=false (events are 1 minute apart)")
	}
	// Banked accrual must sit under the legitimate ceiling (2 gaps × 60s × rate 1.0 = 120).
	if resp.MaxPlausibleAccrual < resp.UnloggedAccrual {
		t.Errorf("unlogged_accrual %v exceeds max_plausible_accrual %v", resp.UnloggedAccrual, resp.MaxPlausibleAccrual)
	}
	if resp.FirstBalanceBefore == nil || *resp.FirstBalanceBefore != 100 {
		t.Errorf("want first_balance_before 100, got %v", resp.FirstBalanceBefore)
	}
	if resp.LastBalanceAfter == nil || *resp.LastBalanceAfter != 35 {
		t.Errorf("want last_balance_after 35, got %v", resp.LastBalanceAfter)
	}
}

func TestEconomy_RowIdentityMismatch_Flagged(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 80, 1.0)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// balance_after should be 70 (100 + 0 - 30); seed a corrupt 80.
	seedGachaLog(t, db, "g1", "p1", 30, 0, 100, 80, t0)

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.Reconciled {
		t.Fatalf("want reconciled=false")
	}
	if len(resp.Anomalies) != 1 || resp.Anomalies[0].Type != "row_identity" {
		t.Fatalf("want exactly one row_identity anomaly, got %+v", resp.Anomalies)
	}
	if resp.Anomalies[0].EventID != "g1" {
		t.Errorf("want anomaly on g1, got %s", resp.Anomalies[0].EventID)
	}
}

func TestEconomy_NegativeContinuity_Flagged(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 20, 1.0)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedGachaLog(t, db, "g1", "p1", 30, 0, 100, 70, t0)                 // valid row
	seedGachaLog(t, db, "g2", "p1", 30, 0, 50, 20, t0.Add(time.Minute)) // valid row, gap 50-70 = -20 (distinct second)

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.Reconciled {
		t.Fatalf("want reconciled=false")
	}
	found := false
	for _, a := range resp.Anomalies {
		if a.Type == "negative_continuity" && a.EventID == "g2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a negative_continuity anomaly on g2, got %+v", resp.Anomalies)
	}
}

// A valid same-second chain (one event's after == the next event's before, ordered by
// descending balance) must reconcile clean even though the wall-clock order is not stored.
func TestEconomy_SameSecondChain_Reconciled(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 40, 1.0)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// Same second; the chain links: 100→70 then 70→40 (descending-balance order recovers it).
	seedGachaLog(t, db, "g1", "p1", 30, 0, 100, 70, t0)
	seedVowLog(t, db, "v1", "p1", 30, 0, 70, 40, t0)

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !resp.SameSecondEvents {
		t.Errorf("want same_second_events=true")
	}
	if !resp.Reconciled {
		t.Fatalf("want reconciled=true for a valid same-second chain, got anomalies %+v", resp.Anomalies)
	}
	if resp.TotalEvents != 2 {
		t.Errorf("want 2 events, got %d", resp.TotalEvents)
	}
}

// A zero-cost infinite-debug vow can RAISE the balance, so a same-second pair may be in
// ascending-balance order. Either ordering must be allowed; this must not false-positive.
func TestEconomy_SameSecondZeroCostVow_NoFalsePositive(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 101, 1.0)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// Debug vows: cost 0. A accrues +1 (100→101), then B (101→101), same second.
	seedVowLog(t, db, "a", "p1", 0, 1, 100, 101, t0)
	seedVowLog(t, db, "b", "p1", 0, 0, 101, 101, t0)

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !resp.SameSecondEvents {
		t.Errorf("want same_second_events=true")
	}
	if !resp.Reconciled {
		t.Fatalf("want reconciled=true (reverse order is consistent), got anomalies %+v", resp.Anomalies)
	}
}

// Regression for an intra-second drain that group-min/max bounds missed: a later second
// holds two events whose internal sequence drops the balance unexplained (85 → 70).
func TestEconomy_MultiRowSecondInternalDrain_Flagged(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 40, 1.0)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedGachaLog(t, db, "g0", "p1", 10, 0, 100, 90, t0) // 100 -> 90 (valid)
	t1 := t0.Add(time.Minute)
	// Two events sharing t1; descending-balance order is a (90→85) then b (70→40),
	// so the a→b transition exposes an unexplained 85 → 70 drop.
	seedVowLog(t, db, "a", "p1", 5, 0, 90, 85, t1)
	seedVowLog(t, db, "b", "p1", 30, 0, 70, 40, t1)

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.Reconciled {
		t.Fatalf("want reconciled=false on an intra-second drain, got %+v", resp.Anomalies)
	}
	found := false
	for _, an := range resp.Anomalies {
		if an.Type == "negative_continuity" && an.EventID == "b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want negative_continuity on b, got %+v", resp.Anomalies)
	}
}

// A balance jump far larger than elapsed × rate cannot be passive accrual and must be
// flagged, even though every row identity holds.
func TestEconomy_ExcessAccrual_Flagged(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 999_999_970, 1.0)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedGachaLog(t, db, "g1", "p1", 30, 0, 100, 70, t0) // valid row
	// 60s later balance_before jumped to 1e9 — far beyond 60×1.0 plausible accrual.
	seedGachaLog(t, db, "g2", "p1", 30, 0, 1_000_000_000, 999_999_970, t0.Add(time.Minute))

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.Reconciled {
		t.Fatalf("want reconciled=false on an impossible balance jump")
	}
	found := false
	for _, a := range resp.Anomalies {
		if a.Type == "excess_accrual" && a.EventID == "g2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want an excess_accrual anomaly on g2, got %+v", resp.Anomalies)
	}
}

// A valid row near the NUMERIC(12,2) ceiling must not be flagged just because float64
// round-off at that magnitude exceeds the absolute epsilon.
func TestEconomy_HighBalanceRowIdentity_NoFalsePositive(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 9999999999.96, 1.0)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// Identity holds exactly: 9999999999.97 + 0.04 - 0.05 = 9999999999.96; the float
	// re-evaluation residual (~1.9e-6) exceeds the absolute epsilon but not the scaled one.
	seedGachaLog(t, db, "g1", "p1", 0.05, 0.04, 9999999999.97, 9999999999.96, t0)

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !resp.Reconciled {
		t.Fatalf("want reconciled=true for a valid high-balance row, got anomalies %+v", resp.Anomalies)
	}
}

// unlogged_accrual must never exceed max_plausible_accrual, even for short gaps where the
// per-gap rounding margin would otherwise let banked accrual outrun bare elapsed×rate.
func TestEconomy_UnloggedWithinPlausibleCeiling(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 14, 1.0)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	seedGachaLog(t, db, "g1", "p1", 30, 0, 100, 70, t0)
	seedGachaLog(t, db, "g2", "p1", 30, 0, 72, 42, t0.Add(time.Second)) // gap +2 within 1s ceiling
	seedGachaLog(t, db, "g3", "p1", 30, 0, 44, 14, t0.Add(2*time.Second))

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !resp.Reconciled {
		t.Fatalf("want reconciled=true, got anomalies %+v", resp.Anomalies)
	}
	if resp.UnloggedAccrual > resp.MaxPlausibleAccrual+1e-9 {
		t.Fatalf("unlogged_accrual %v must not exceed max_plausible_accrual %v",
			resp.UnloggedAccrual, resp.MaxPlausibleAccrual)
	}
}

// A vow-only player exercises the vow UNION subquery / column mapping in isolation.
func TestEconomy_VowOnly_RowIdentityFlagged(t *testing.T) {
	db := newTestDB(t)
	seedPlayer(t, db, "p1", 100, 1.0)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// before + 0 - 30 = 70, but seed a corrupt 80.
	seedVowLog(t, db, "v1", "p1", 30, 0, 100, 80, t0)

	w, resp := callEconomy(db, "?player_id=p1")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if resp.GachaPulls != 0 || resp.VowCount != 1 {
		t.Errorf("counts: gacha=%d vow=%d", resp.GachaPulls, resp.VowCount)
	}
	if resp.Reconciled {
		t.Fatalf("want reconciled=false")
	}
	if len(resp.Anomalies) != 1 || resp.Anomalies[0].Type != "row_identity" || resp.Anomalies[0].EventKind != "vow" {
		t.Fatalf("want one vow row_identity anomaly, got %+v", resp.Anomalies)
	}
}
