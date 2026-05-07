package achievement

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---- DB mock ----

type mockHandlerDB struct {
	queryRowFns []func() store.Row
	queryFn     func() (store.Rows, error)
	execFns     []func() (store.Result, error)
	callOrder   []string
}

func (m *mockHandlerDB) QueryRow(_ context.Context, _ string, _ ...any) store.Row {
	m.callOrder = append(m.callOrder, "QueryRow")
	fn := m.queryRowFns[0]
	m.queryRowFns = m.queryRowFns[1:]
	return fn()
}

func (m *mockHandlerDB) Query(_ context.Context, _ string, _ ...any) (store.Rows, error) {
	return m.queryFn()
}

func (m *mockHandlerDB) Exec(_ context.Context, _ string, _ ...any) (store.Result, error) {
	m.callOrder = append(m.callOrder, "Exec")
	fn := m.execFns[0]
	m.execFns = m.execFns[1:]
	return fn()
}

// ---- mockRows ----

type mockRows struct {
	scanFns []func(dest ...any) error
	idx     int
	err     error
}

func (r *mockRows) Next() bool             { return r.idx < len(r.scanFns) }
func (r *mockRows) Close() error           { return nil }
func (r *mockRows) Err() error             { return r.err }
func (r *mockRows) Scan(dest ...any) error { fn := r.scanFns[r.idx]; r.idx++; return fn(dest...) }

// ---- helpers ----

// mockExecResult is used by execOK/execErr to satisfy store.Result.
type mockExecResult struct{ affected int64 }

func (m mockExecResult) RowsAffected() (int64, error) { return m.affected, nil }

// rowErrH wraps an error in a mockRow (reuses the type from conditions_test.go).
func rowErrH(err error) mockRow { return rowErr(err) }

// rowScan returns a mockRow that calls scanFn.
func rowScan(fn func(dest ...any) error) mockRow {
	return mockRow{scanFn: fn}
}

func execOK() func() (store.Result, error) {
	return func() (store.Result, error) { return mockExecResult{affected: 1}, nil }
}

// execAffected returns a result with the given rows-affected count (used for
// race-condition tests where INSERT ON CONFLICT DO NOTHING returns 0).
func execAffected(n int64) func() (store.Result, error) {
	return func() (store.Result, error) { return mockExecResult{affected: n}, nil }
}

func execErr(err error) func() (store.Result, error) {
	return func() (store.Result, error) { return nil, err }
}

// ---- Steam mock ----

type mockSteam struct {
	err    error
	called bool
}

func (m *mockSteam) SetAchievement(_ context.Context, _, _ string) error {
	m.called = true
	return m.err
}

// ---- test router helper ----

func newTestRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.POST("/achievement/unlock", func(c *gin.Context) {
		c.Set("player_id", "player-uuid-1")
		h.Unlock(c)
	})
	return r
}

func doUnlock(r *gin.Engine, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/achievement/unlock",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// ---- tests ----

func TestUnlock_AlreadyUnlocked(t *testing.T) {
	kv := kvstore.NewMemStore()

	db := &mockHandlerDB{
		queryRowFns: []func() store.Row{
			// existing row found — dest[0] is store.ScanTime scanner, dest[1] is *bool
			func() store.Row {
				return rowScan(func(dest ...any) error {
					*(dest[1].(*bool)) = true
					return nil
				})
			},
		},
	}
	h := &Handler{db: db, kv: kv, steam: &mockSteam{}}
	w := doUnlock(newTestRouter(h), `{"achievement_id":"ACH_FIRST_PULL"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["already_unlocked"] != true {
		t.Fatal("expected already_unlocked: true")
	}
}

func TestUnlock_ConditionNotMet(t *testing.T) {
	kv := kvstore.NewMemStore()

	db := &mockHandlerDB{
		queryRowFns: []func() store.Row{
			// no existing row
			func() store.Row { return rowErrH(sql.ErrNoRows) },
			// condition check: count = 0 → not met
			func() store.Row { return rowInt(0) },
		},
	}
	h := &Handler{db: db, kv: kv, steam: &mockSteam{}}
	w := doUnlock(newTestRouter(h), `{"achievement_id":"ACH_FIRST_PULL"}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != "CONDITION_NOT_MET" {
		t.Fatalf("expected CONDITION_NOT_MET, got %v", resp["code"])
	}
}

func TestUnlock_SteamSuccess(t *testing.T) {
	kv := kvstore.NewMemStore()
	steam := &mockSteam{}

	db := &mockHandlerDB{
		queryRowFns: []func() store.Row{
			// no existing row
			func() store.Row { return rowErrH(sql.ErrNoRows) },
			// condition check: count = 1 → met
			func() store.Row { return rowInt(1) },
			// steam_id lookup
			func() store.Row {
				return rowScan(func(dest ...any) error {
					*dest[0].(*string) = "76561198000000001"
					return nil
				})
			},
		},
		execFns: []func() (store.Result, error){
			execOK(), // INSERT player_achievements
			execOK(), // UPDATE steam_synced = true
		},
	}
	h := &Handler{db: db, kv: kv, steam: steam}
	w := doUnlock(newTestRouter(h), `{"achievement_id":"ACH_FIRST_PULL"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["steam_synced"] != true {
		t.Fatal("expected steam_synced: true")
	}
	if !steam.called {
		t.Fatal("steam SetAchievement was not called")
	}
}

func TestUnlock_SteamFailure_QueuedForRetry(t *testing.T) {
	kv := kvstore.NewMemStore()
	steamErr := errors.New("steam unavailable")
	steam := &mockSteam{err: steamErr}

	db := &mockHandlerDB{
		queryRowFns: []func() store.Row{
			func() store.Row { return rowErrH(sql.ErrNoRows) },
			func() store.Row { return rowInt(1) },
			func() store.Row {
				return rowScan(func(dest ...any) error {
					*dest[0].(*string) = "76561198000000001"
					return nil
				})
			},
		},
		execFns: []func() (store.Result, error){
			execOK(), // INSERT player_achievements
			execOK(), // INSERT achievement_retry_queue
		},
	}
	h := &Handler{db: db, kv: kv, steam: steam}
	w := doUnlock(newTestRouter(h), `{"achievement_id":"ACH_FIRST_PULL"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["steam_synced"] != false {
		t.Fatal("expected steam_synced: false")
	}

	// Verify KV queue entry was pushed
	item, err := kv.RPop(context.Background(), "ach:retry")
	if err != nil || item == "" {
		t.Fatalf("expected 1 item in ach:retry queue, RPop err: %v, item: %q", err, item)
	}
}

// TestUnlock_DBInsertBeforeSteam verifies that the DB INSERT (Exec)
// is called before the Steam API.
func TestUnlock_DBInsertBeforeSteam(t *testing.T) {
	kv := kvstore.NewMemStore()

	var insertCalled, steamCalled bool
	var insertOrder, steamOrder int
	callCount := 0

	db := &mockHandlerDB{
		queryRowFns: []func() store.Row{
			func() store.Row { return rowErrH(sql.ErrNoRows) },
			func() store.Row { return rowInt(1) },
			func() store.Row {
				return rowScan(func(dest ...any) error {
					*dest[0].(*string) = "76561198000000001"
					return nil
				})
			},
		},
		execFns: []func() (store.Result, error){
			func() (store.Result, error) {
				callCount++
				insertCalled = true
				insertOrder = callCount
				return mockExecResult{affected: 1}, nil
			},
			execOK(), // UPDATE steam_synced
		},
	}

	wrappedSteam := &orderTrackingSteam{
		inner: &mockSteam{},
		onCall: func() {
			callCount++
			steamCalled = true
			steamOrder = callCount
		},
	}

	h := &Handler{db: db, kv: kv, steam: wrappedSteam}
	doUnlock(newTestRouter(h), `{"achievement_id":"ACH_FIRST_PULL"}`)

	if !insertCalled || !steamCalled {
		t.Fatal("expected both INSERT and Steam to be called")
	}
	if insertOrder >= steamOrder {
		t.Fatalf("DB INSERT (order %d) must happen before Steam call (order %d)", insertOrder, steamOrder)
	}
}

// orderTrackingSteam wraps a SteamAchievementClient to record when it's called.
type orderTrackingSteam struct {
	inner  SteamAchievementClient
	onCall func()
}

func (s *orderTrackingSteam) SetAchievement(ctx context.Context, steamID, achievementID string) error {
	s.onCall()
	return s.inner.SetAchievement(ctx, steamID, achievementID)
}

func TestUnlock_SteamFailure_DBRecordPersists(t *testing.T) {
	kv := kvstore.NewMemStore()

	var insertedIntoDB bool
	db := &mockHandlerDB{
		queryRowFns: []func() store.Row{
			func() store.Row { return rowErrH(sql.ErrNoRows) },
			func() store.Row { return rowInt(1) },
			func() store.Row {
				return rowScan(func(dest ...any) error {
					*dest[0].(*string) = "76561198000000001"
					return nil
				})
			},
		},
		execFns: []func() (store.Result, error){
			func() (store.Result, error) {
				insertedIntoDB = true
				return mockExecResult{affected: 1}, nil
			},
			execOK(), // INSERT achievement_retry_queue
		},
	}

	h := &Handler{db: db, kv: kv, steam: &mockSteam{err: errors.New("steam down")}}
	w := doUnlock(newTestRouter(h), `{"achievement_id":"ACH_FIRST_PULL"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !insertedIntoDB {
		t.Fatal("player_achievements DB record must exist even when Steam fails")
	}
}

// TestUnlock_SteamSuccess_DBUpdateFails verifies that if Steam succeeds but the
// steam_synced DB update fails, the response returns steam_synced: false and
// the entry is queued for retry — never returning steam_synced: true with a stale DB.
func TestUnlock_SteamSuccess_DBUpdateFails(t *testing.T) {
	kv := kvstore.NewMemStore()
	dbErr := errors.New("db write failed")

	db := &mockHandlerDB{
		queryRowFns: []func() store.Row{
			func() store.Row { return rowErrH(sql.ErrNoRows) },
			func() store.Row { return rowInt(1) },
			func() store.Row {
				return rowScan(func(dest ...any) error {
					*dest[0].(*string) = "76561198000000001"
					return nil
				})
			},
		},
		execFns: []func() (store.Result, error){
			execOK(),       // INSERT player_achievements
			execErr(dbErr), // UPDATE steam_synced = true → fails
			execOK(),       // INSERT achievement_retry_queue
		},
	}
	h := &Handler{db: db, kv: kv, steam: &mockSteam{}}
	w := doUnlock(newTestRouter(h), `{"achievement_id":"ACH_FIRST_PULL"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["steam_synced"] != false {
		t.Fatal("expected steam_synced: false when DB update fails after Steam success")
	}

	// Must be queued for retry
	item, err := kv.RPop(context.Background(), "ach:retry")
	if err != nil || item == "" {
		t.Fatalf("expected 1 item in ach:retry queue, RPop err: %v, item: %q", err, item)
	}
}

// TestUnlock_RaceCondition_AlreadyInserted simulates the race where the existing-row
// SELECT returns nothing but INSERT ON CONFLICT DO NOTHING reports zero rows because
// another concurrent request inserted in between. The handler must respond as
// already_unlocked, not 500.
func TestUnlock_RaceCondition_AlreadyInserted(t *testing.T) {
	kv := kvstore.NewMemStore()
	steam := &mockSteam{}

	now := time.Now().UTC().Truncate(time.Second)
	db := &mockHandlerDB{
		queryRowFns: []func() store.Row{
			// 1. existing-row check → not found
			func() store.Row { return rowErrH(sql.ErrNoRows) },
			// 2. condition check: count = 1 → met
			func() store.Row { return rowInt(1) },
			// 3. steam_id lookup
			func() store.Row {
				return rowScan(func(dest ...any) error {
					*dest[0].(*string) = "76561198000000001"
					return nil
				})
			},
			// 4. refetch after INSERT-conflict
			func() store.Row {
				return rowScan(func(dest ...any) error {
					if sc, ok := dest[0].(sql.Scanner); ok {
						sc.Scan(now.Format(time.RFC3339))
					}
					*dest[1].(*bool) = true
					return nil
				})
			},
		},
		execFns: []func() (store.Result, error){
			execAffected(0), // INSERT ON CONFLICT DO NOTHING — race lost
		},
	}
	h := &Handler{db: db, kv: kv, steam: steam}
	w := doUnlock(newTestRouter(h), `{"achievement_id":"ACH_FIRST_PULL"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["already_unlocked"] != true {
		t.Fatalf("expected already_unlocked: true, got %v", resp)
	}
	if resp["steam_synced"] != true {
		t.Fatalf("expected steam_synced: true (winner already synced), got %v", resp)
	}
	if steam.called {
		t.Fatal("Steam must not be called again on race-loss path")
	}
}

func TestUnlock_MissingAchievementID(t *testing.T) {
	kv := kvstore.NewMemStore()
	h := &Handler{db: &mockHandlerDB{}, kv: kv, steam: &mockSteam{}}
	w := doUnlock(newTestRouter(h), `{}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != "INVALID_REQUEST" {
		t.Fatalf("expected INVALID_REQUEST, got %v", resp["code"])
	}
}

// ---- List tests ----

func newListRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.GET("/achievement/list", func(c *gin.Context) {
		c.Set("player_id", "player-uuid-1")
		h.List(c)
	})
	return r
}

func doList(r *gin.Engine) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/achievement/list", nil)
	r.ServeHTTP(w, req)
	return w
}

func TestList_AllSix_NoUnlocks(t *testing.T) {
	kv := kvstore.NewMemStore()

	db := &mockHandlerDB{
		queryFn: func() (store.Rows, error) {
			return &mockRows{}, nil // empty result set
		},
	}
	h := &Handler{db: db, kv: kv, steam: &mockSteam{}}
	w := doList(newListRouter(h))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	items := resp["achievements"].([]any)
	if len(items) != 6 {
		t.Fatalf("expected 6 achievements, got %d", len(items))
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["unlocked"] != false {
			t.Errorf("expected unlocked: false for %v", item["achievement_id"])
		}
		if item["unlocked_at"] != nil {
			t.Errorf("expected unlocked_at: null for %v", item["achievement_id"])
		}
		if item["steam_synced"] != false {
			t.Errorf("expected steam_synced: false for %v", item["achievement_id"])
		}
	}
}

func TestList_PartialUnlocks(t *testing.T) {
	kv := kvstore.NewMemStore()
	now := time.Now().UTC().Truncate(time.Second)

	db := &mockHandlerDB{
		queryFn: func() (store.Rows, error) {
			return &mockRows{
				scanFns: []func(dest ...any) error{
					// ACH_FIRST_PULL: dest[0]=*string, dest[1]=sql.Scanner(ScanTime), dest[2]=*bool
					func(dest ...any) error {
						*dest[0].(*string) = AchFirstPull
						if sc, ok := dest[1].(sql.Scanner); ok {
							sc.Scan(now.Format(time.RFC3339))
						}
						*dest[2].(*bool) = true
						return nil
					},
					// ACH_COLLECTOR: unlocked, steam_synced = false
					func(dest ...any) error {
						*dest[0].(*string) = AchCollector
						if sc, ok := dest[1].(sql.Scanner); ok {
							sc.Scan(now.Format(time.RFC3339))
						}
						*dest[2].(*bool) = false
						return nil
					},
				},
			}, nil
		},
	}
	h := &Handler{db: db, kv: kv, steam: &mockSteam{}}
	w := doList(newListRouter(h))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)

	items := resp["achievements"].([]any)
	if len(items) != 6 {
		t.Fatalf("expected 6 achievements, got %d", len(items))
	}

	byID := make(map[string]map[string]any, 6)
	for _, raw := range items {
		item := raw.(map[string]any)
		byID[item["achievement_id"].(string)] = item
	}

	fp := byID[AchFirstPull]
	if fp["unlocked"] != true {
		t.Error("ACH_FIRST_PULL: expected unlocked: true")
	}
	if fp["unlocked_at"] == nil {
		t.Error("ACH_FIRST_PULL: expected unlocked_at != null")
	}
	if fp["steam_synced"] != true {
		t.Error("ACH_FIRST_PULL: expected steam_synced: true")
	}

	col := byID[AchCollector]
	if col["unlocked"] != true {
		t.Error("ACH_COLLECTOR: expected unlocked: true")
	}
	if col["steam_synced"] != false {
		t.Error("ACH_COLLECTOR: expected steam_synced: false")
	}

	leg := byID[AchLegendary]
	if leg["unlocked"] != false {
		t.Error("ACH_LEGENDARY: expected unlocked: false")
	}
	if leg["unlocked_at"] != nil {
		t.Error("ACH_LEGENDARY: expected unlocked_at: null")
	}
}

func TestList_OrderMatchesAllAchievements(t *testing.T) {
	kv := kvstore.NewMemStore()

	db := &mockHandlerDB{
		queryFn: func() (store.Rows, error) { return &mockRows{}, nil },
	}
	h := &Handler{db: db, kv: kv, steam: &mockSteam{}}
	w := doList(newListRouter(h))

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	items := resp["achievements"].([]any)

	for i, id := range allAchievements {
		got := items[i].(map[string]any)["achievement_id"].(string)
		if got != id {
			t.Errorf("position %d: expected %s, got %s", i, id, got)
		}
	}
}
