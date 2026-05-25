package gacha

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

// mockRows implements store.Rows.
type mockRows struct {
	scanFns []func(dest ...any) error
	idx     int
	err     error
}

func (r *mockRows) Next() bool             { return r.idx < len(r.scanFns) }
func (r *mockRows) Scan(dest ...any) error { e := r.scanFns[r.idx](dest...); r.idx++; return e }
func (r *mockRows) Err() error             { return r.err }
func (r *mockRows) Close() error           { return nil }

func init() { gin.SetMode(gin.TestMode) }

// mockRow implements store.Row.
type mockRow struct {
	scanFn func(dest ...any) error
}

func (r *mockRow) Scan(dest ...any) error { return r.scanFn(dest...) }

func rowErrNoRows() store.Row {
	return &mockRow{scanFn: func(...any) error { return sql.ErrNoRows }}
}

func rowTimePtrResult(val *time.Time) store.Row {
	return &mockRow{scanFn: func(dest ...any) error {
		if sc, ok := dest[0].(sql.Scanner); ok {
			if val == nil {
				return sc.Scan(nil)
			}
			return sc.Scan(val.Format(time.RFC3339))
		}
		*(dest[0].(**time.Time)) = val
		return nil
	}}
}

func rowFloat64Result(v float64) store.Row {
	return &mockRow{scanFn: func(dest ...any) error {
		*(dest[0].(*float64)) = v
		return nil
	}}
}

// rowTimeStrResult mocks an UPDATE ... RETURNING <ts column> response.
// Drives a store.ScanTime scanner with an RFC3339 string.
func rowTimeStrResult(t time.Time) store.Row {
	return &mockRow{scanFn: func(dest ...any) error {
		if sc, ok := dest[0].(sql.Scanner); ok {
			return sc.Scan(t.UTC().Format(time.RFC3339))
		}
		return errors.New("rowTimeStrResult: dest[0] not sql.Scanner")
	}}
}

// mockResult implements store.Result.
type mockResult struct {
	affected int64
}

func (r mockResult) RowsAffected() (int64, error) { return r.affected, nil }

// okExec returns a function that produces a successful store.Result.
func okExec(affected int64) func() (store.Result, error) {
	return func() (store.Result, error) { return mockResult{affected}, nil }
}

// failExec returns a function that always returns an error.
func failExec(err error) func() (store.Result, error) {
	return func() (store.Result, error) { return nil, err }
}

// mockTx implements store.Tx.
type mockTx struct {
	queryRowQueue []store.Row
	queryRowIdx   int
	execQueue     []func() (store.Result, error)
	execIdx       int
	commitErr     error
	Committed     bool
	RolledBack    bool
}

func (m *mockTx) QueryRow(_ context.Context, _ string, _ ...any) store.Row {
	r := m.queryRowQueue[m.queryRowIdx]
	m.queryRowIdx++
	return r
}
func (m *mockTx) Query(_ context.Context, _ string, _ ...any) (store.Rows, error) {
	return &mockRows{}, nil
}
func (m *mockTx) Exec(_ context.Context, _ string, _ ...any) (store.Result, error) {
	fn := m.execQueue[m.execIdx]
	m.execIdx++
	return fn()
}
func (m *mockTx) Commit(_ context.Context) error   { m.Committed = true; return m.commitErr }
func (m *mockTx) Rollback(_ context.Context) error { m.RolledBack = true; return nil }

// mockDB implements store.DB.
// queryRowFns is a queue; each QueryRow call pops the next entry.
// Falls back to rowErrNoRows if the queue is exhausted.
type mockDB struct {
	tx          store.Tx
	beginErr    error
	queryRowFns []func() store.Row
	qrIdx       int
	queryFn     func() (store.Rows, error)
}

func (m *mockDB) Begin(_ context.Context) (store.Tx, error) { return m.tx, m.beginErr }
func (m *mockDB) QueryRow(_ context.Context, _ string, _ ...any) store.Row {
	if m.qrIdx < len(m.queryRowFns) {
		fn := m.queryRowFns[m.qrIdx]
		m.qrIdx++
		return fn()
	}
	return rowErrNoRows()
}
func (m *mockDB) Query(_ context.Context, _ string, _ ...any) (store.Rows, error) {
	if m.queryFn != nil {
		return m.queryFn()
	}
	return &mockRows{}, nil
}
func (m *mockDB) Exec(_ context.Context, _ string, _ ...any) (store.Result, error) {
	return mockResult{}, nil
}
func (m *mockDB) Ping(_ context.Context) error { return nil }

// callPull call helper (bypasses HTTP layer).
// slot_index 는 0 (첫 슬롯) 으로 고정 — 본 unit 테스트는 가챠 트랜잭션 로직에 집중하며,
// slot 검증 자체는 별도 케이스에서 다룬다.
func callPull(h *Handler, playerID string) *httptest.ResponseRecorder {
	return callPullWithSlot(h, playerID, 0)
}

func callPullWithSlot(h *Handler, playerID string, slotIndex int) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("player_id", playerID)
	body := strings.NewReader(fmt.Sprintf(`{"slot_index":%d}`, slotIndex))
	c.Request = httptest.NewRequest(http.MethodPost, "/gacha/pull", body)
	c.Request.Header.Set("Content-Type", "application/json")
	h.Pull(c)
	return w
}

// completeSlotStartedAt returns a started_at value far enough in the past that
// cairn.Derive marks the slot as complete (>= MaxLayers × SpawnInterval ago).
func completeSlotStartedAt() time.Time {
	return time.Now().UTC().Add(-time.Hour)
}

// --- cooldown / getNextGachaAt tests ---

func TestGetNextGachaAt_RedisCacheHit(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()

	future := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	kv.Set(ctx, "gacha:cooldown:p1", future.Format(time.RFC3339), 10*time.Minute)

	h := &Handler{db: &mockDB{}, kv: kv, cfg: DefaultConfig}
	got, err := h.getNextGachaAt(ctx, "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || !got.Equal(future) {
		t.Errorf("want %v, got %v", future, got)
	}
}

func TestGetNextGachaAt_RedisMiss_DBFallback(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()

	future := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Second)
	db := &mockDB{queryRowFns: []func() store.Row{func() store.Row { return rowTimePtrResult(&future) }}}

	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	got, err := h.getNextGachaAt(ctx, "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || !got.Equal(future) {
		t.Errorf("want %v, got %v", future, got)
	}
	// KV should be repopulated
	exists, _ := kv.Exists(ctx, "gacha:cooldown:p1")
	if !exists {
		t.Error("cooldown key should be repopulated after DB fallback")
	}
}

func TestGetNextGachaAt_RedisMiss_DBNull(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()

	db := &mockDB{queryRowFns: []func() store.Row{func() store.Row { return rowTimePtrResult(nil) }}}
	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}

	got, err := h.getNextGachaAt(ctx, "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("want nil (no cooldown), got %v", got)
	}
}

// --- Pull handler path tests ---

func TestPull_CooldownActive(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()

	future := time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second)
	kv.Set(ctx, "gacha:cooldown:p1", future.Format(time.RFC3339), 15*time.Minute)

	h := &Handler{db: &mockDB{}, kv: kv, cfg: DefaultConfig}
	w := callPull(h, "p1")

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "COOLDOWN_ACTIVE") {
		t.Fatalf("want COOLDOWN_ACTIVE: %s", w.Body.String())
	}
}

// --- execPull transaction tests ---

func TestExecPull_InsufficientPoints(t *testing.T) {
	kv := kvstore.NewMemStore()

	// UPDATE-RETURNING with WHERE pts >= cost returns no rows when balance is short.
	tx := &mockTx{
		queryRowQueue: []store.Row{
			rowTimeStrResult(completeSlotStartedAt()), // cairn slot validate (complete)
			rowErrNoRows(),                            // deduct returns no rows → InsufficientPoints
		},
		execQueue: []func() (store.Result, error){},
	}
	db := &mockDB{tx: tx}

	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	w := callPull(h, "p1")

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "INSUFFICIENT_POINTS") {
		t.Fatalf("want INSUFFICIENT_POINTS: %s", w.Body.String())
	}
	if !tx.RolledBack {
		t.Error("transaction should have been rolled back")
	}
}

func TestExecPull_RollbackOnInventoryError(t *testing.T) {
	kv := kvstore.NewMemStore()

	dbErr := errors.New("db: inventory insert failed")
	tx := &mockTx{
		queryRowQueue: []store.Row{
			rowTimeStrResult(completeSlotStartedAt()), // cairn slot validate
			rowFloat64Result(100),                     // deduct → 200 - 100
		},
		execQueue: []func() (store.Result, error){
			failExec(dbErr), // inventory INSERT — fails → should rollback
		},
	}
	db := &mockDB{tx: tx}

	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	w := callPull(h, "p1")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
	if !tx.RolledBack {
		t.Error("transaction should have been rolled back on inventory error")
	}
	if tx.Committed {
		t.Error("transaction must not be committed on error")
	}
}

func TestExecPull_RollbackOnLogError(t *testing.T) {
	kv := kvstore.NewMemStore()

	dbErr := errors.New("db: gacha_logs insert failed")
	tx := &mockTx{
		queryRowQueue: []store.Row{
			rowTimeStrResult(completeSlotStartedAt()), // cairn slot validate
			rowFloat64Result(100),                     // deduct → post-deduct balance
			rowTimeStrResult(time.Now().UTC()),        // next_gacha_at RETURNING last_sync_at
		},
		execQueue: []func() (store.Result, error){
			okExec(1),       // inventory — new item (non-duplicate, no refund path)
			okExec(1),       // wish_cairn_slots reset
			failExec(dbErr), // gacha_logs INSERT — fails
		},
	}
	db := &mockDB{tx: tx}

	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	w := callPull(h, "p1")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
	if !tx.RolledBack {
		t.Error("transaction should have been rolled back on gacha_logs error")
	}
	if tx.Committed {
		t.Error("transaction must not be committed on error")
	}
}

func TestExecPull_NewItem_Committed(t *testing.T) {
	kv := kvstore.NewMemStore()

	tx := &mockTx{
		queryRowQueue: []store.Row{
			rowTimeStrResult(completeSlotStartedAt()), // cairn slot validate
			rowFloat64Result(400),                     // deduct → 500-100
			rowTimeStrResult(time.Now().UTC()),        // next_gacha_at RETURNING last_sync_at
		},
		execQueue: []func() (store.Result, error){
			okExec(1), // inventory — new item (1 row inserted)
			okExec(1), // wish_cairn_slots reset
			okExec(1), // gacha_logs
		},
	}
	db := &mockDB{tx: tx}

	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	w := callPull(h, "p1")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !tx.Committed {
		t.Error("transaction should have been committed")
	}
	body := w.Body.String()
	if !strings.Contains(body, `"is_duplicate":false`) {
		t.Errorf("want is_duplicate:false in body: %s", body)
	}
	if !strings.Contains(body, "next_gacha_at") {
		t.Errorf("want next_gacha_at in body: %s", body)
	}
	if !strings.Contains(body, `"balance_after":400`) {
		t.Errorf("want balance_after:400 in body: %s", body)
	}
	if !strings.Contains(body, "last_sync_at") {
		t.Errorf("want last_sync_at in body: %s", body)
	}
}

// --- Status handler tests ---

func callStatus(h *Handler, playerID string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("player_id", playerID)
	c.Request = httptest.NewRequest(http.MethodGet, "/gacha/status", nil)
	h.Status(c)
	return w
}

func TestStatus_CooldownActive(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()

	future := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	kv.Set(ctx, "gacha:cooldown:p1", future.Format(time.RFC3339), 10*time.Minute)

	// KV hit → getNextGachaAt makes no DB call.
	db := &mockDB{}
	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	w := callStatus(h, "p1")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"can_pull":false`) {
		t.Errorf("want can_pull:false: %s", body)
	}
	if !strings.Contains(body, "next_gacha_at") {
		t.Errorf("want next_gacha_at in body: %s", body)
	}
}

func TestStatus_NoCooldown(t *testing.T) {
	kv := kvstore.NewMemStore()

	// KV miss, DB returns null next_gacha_at → no cooldown.
	db := &mockDB{queryRowFns: []func() store.Row{
		func() store.Row { return rowTimePtrResult(nil) }, // next_gacha_at (DB fallback)
	}}
	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	w := callStatus(h, "p1")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"can_pull":true`) {
		t.Errorf("want can_pull:true: %s", body)
	}
	if !strings.Contains(body, `"next_gacha_at":null`) {
		t.Errorf("want next_gacha_at:null: %s", body)
	}
}

func TestStatus_RedisMiss_DBFallbackCooldown(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()

	future := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)

	// KV miss → DB returns future next_gacha_at → cooldown active.
	db := &mockDB{queryRowFns: []func() store.Row{
		func() store.Row { return rowTimePtrResult(&future) }, // next_gacha_at (DB fallback)
	}}
	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	w := callStatus(h, "p1")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"can_pull":false`) {
		t.Errorf("want can_pull:false: %s", body)
	}
	// KV should be repopulated by getNextGachaAt
	exists, _ := kv.Exists(ctx, "gacha:cooldown:p1")
	if !exists {
		t.Error("cooldown key should be repopulated after DB fallback")
	}
}

// --- Logs handler tests ---

func callLogs(h *Handler, playerID, query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("player_id", playerID)
	c.Request = httptest.NewRequest(http.MethodGet, "/gacha/logs?"+query, nil)
	h.Logs(c)
	return w
}

func makeLogScanFn(itemID, rarity string, isDup bool, refund, cost float64, pulledAt time.Time) func(dest ...any) error {
	return func(dest ...any) error {
		*(dest[0].(*string))  = itemID
		*(dest[1].(*string))  = rarity
		*(dest[2].(*bool))    = isDup
		*(dest[3].(*float64)) = refund
		*(dest[4].(*float64)) = cost
		if sc, ok := dest[5].(sql.Scanner); ok {
			return sc.Scan(pulledAt.Format(time.RFC3339))
		}
		*(dest[5].(*time.Time)) = pulledAt
		return nil
	}
}

func TestLogs_DefaultParams(t *testing.T) {
	kv := kvstore.NewMemStore()

	now := time.Now().UTC()
	db := &mockDB{
		queryRowFns: []func() store.Row{
			func() store.Row {
				return &mockRow{scanFn: func(dest ...any) error { *(dest[0].(*int64)) = 2; return nil }}
			},
		},
		queryFn: func() (store.Rows, error) {
			return &mockRows{scanFns: []func(dest ...any) error{
				makeLogScanFn("rare_gem_01", "rare", false, 0, 100, now),
				makeLogScanFn("common_stone_01", "common", true, 0, 100, now.Add(-time.Minute)),
			}}, nil
		},
	}
	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	w := callLogs(h, "p1", "")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"total":2`) {
		t.Errorf("want total:2: %s", body)
	}
	if !strings.Contains(body, "rare_gem_01") || !strings.Contains(body, "common_stone_01") {
		t.Errorf("want both items in body: %s", body)
	}
}

func TestLogs_EmptyResult(t *testing.T) {
	kv := kvstore.NewMemStore()

	db := &mockDB{
		queryRowFns: []func() store.Row{
			func() store.Row {
				return &mockRow{scanFn: func(dest ...any) error { *(dest[0].(*int64)) = 0; return nil }}
			},
		},
		queryFn: func() (store.Rows, error) { return &mockRows{}, nil },
	}
	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	w := callLogs(h, "p1", "")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"total":0`) {
		t.Errorf("want total:0: %s", body)
	}
	if !strings.Contains(body, `"logs":[]`) {
		t.Errorf("want logs:[]: %s", body)
	}
}

func TestLogs_PageAndLimit(t *testing.T) {
	kv := kvstore.NewMemStore()

	now := time.Now().UTC()
	db := &mockDB{
		queryRowFns: []func() store.Row{
			func() store.Row {
				return &mockRow{scanFn: func(dest ...any) error { *(dest[0].(*int64)) = 5; return nil }}
			},
		},
		queryFn: func() (store.Rows, error) {
			return &mockRows{scanFns: []func(dest ...any) error{
				makeLogScanFn("uncommon_crystal_01", "uncommon", false, 1, 100, now),
			}}, nil
		},
	}
	h := &Handler{db: db, kv: kv, cfg: DefaultConfig}
	w := callLogs(h, "p1", "page=2&limit=1")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"total":5`) {
		t.Errorf("want total:5: %s", body)
	}
	if !strings.Contains(body, "uncommon_crystal_01") {
		t.Errorf("want item in body: %s", body)
	}
}

func TestExecPull_DuplicateItem_Refund(t *testing.T) {
	kv := kvstore.NewMemStore()

	// Force a non-zero refund for every rarity so the refund branch always executes,
	// keeping the QueryRow sequence deterministic across Roll() randomness.
	cfg := GameConfig{
		PullCost: 100,
		RefundPts: map[Rarity]float64{
			Common: 5, Uncommon: 5, Rare: 5, Unique: 5, Legendary: 5,
		},
	}

	tx := &mockTx{
		queryRowQueue: []store.Row{
			rowTimeStrResult(completeSlotStartedAt()), // cairn slot validate
			rowFloat64Result(400),                     // deduct
			rowFloat64Result(405),                     // refund RETURNING (400 + 5)
			rowTimeStrResult(time.Now().UTC()),        // next_gacha_at RETURNING last_sync_at
		},
		execQueue: []func() (store.Result, error){
			okExec(0), // inventory — duplicate (0 rows inserted)
			okExec(1), // wish_cairn_slots reset
			okExec(1), // gacha_logs
		},
	}
	db := &mockDB{tx: tx}

	h := &Handler{db: db, kv: kv, cfg: cfg}
	w := callPull(h, "p1")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"is_duplicate":true`) {
		t.Errorf("want is_duplicate:true: %s", body)
	}
	if !strings.Contains(body, `"refund_points":5`) {
		t.Errorf("want refund_points:5 in body: %s", body)
	}
	if !strings.Contains(body, `"balance_after":405`) {
		t.Errorf("want balance_after:405 in body: %s", body)
	}
}
