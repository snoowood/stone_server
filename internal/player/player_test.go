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
