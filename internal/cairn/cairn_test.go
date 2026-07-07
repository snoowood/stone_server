package cairn

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gensdeis/stone-server/pkg/sqlitedb"
	"github.com/gensdeis/stone-server/pkg/store"
)

// captureDB satisfies store.DB and records every Exec call for assertion.
// Read/Begin paths are unused by InitializeSlots so they return inert values.
type captureDB struct {
	execCalls []capturedExec
}

type capturedExec struct {
	query string
	args  []any
}

func (db *captureDB) Exec(_ context.Context, query string, args ...any) (store.Result, error) {
	db.execCalls = append(db.execCalls, capturedExec{query: query, args: args})
	return capturedResult{}, nil
}

func (db *captureDB) Query(_ context.Context, _ string, _ ...any) (store.Rows, error) {
	return nil, nil
}

func (db *captureDB) QueryRow(_ context.Context, _ string, _ ...any) store.Row { return nil }
func (db *captureDB) Begin(_ context.Context) (store.Tx, error)               { return nil, nil }
func (db *captureDB) Ping(_ context.Context) error                            { return nil }

type capturedResult struct{}

func (capturedResult) RowsAffected() (int64, error) { return 1, nil }

// M4: InitializeSlots 가 SlotCount 개의 INSERT 를 시차로 발행하는지 검증.
// args[1] = slot_index (0..SlotCount-1, 순서), args[2] = started_at RFC3339 string
// (slot k 의 startedAt = now - k × PhaseOffset). 저장형 전환 후에도 started_at stagger
// 시딩 동작(vestige)은 유지된다.
func TestInitializeSlots_EmitsStaggeredInserts(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	db := &captureDB{}

	if err := Default.InitializeSlots(context.Background(), db, "player-1", now); err != nil {
		t.Fatalf("InitializeSlots: %v", err)
	}

	if len(db.execCalls) != Default.SlotCount {
		t.Fatalf("want %d INSERT calls, got %d", Default.SlotCount, len(db.execCalls))
	}

	phase := Default.PhaseOffset()
	for i, call := range db.execCalls {
		if len(call.args) < 3 {
			t.Fatalf("call %d: want >= 3 args, got %d", i, len(call.args))
		}
		gotPlayerID, _ := call.args[0].(string)
		if gotPlayerID != "player-1" {
			t.Errorf("call %d: player_id %q want %q", i, gotPlayerID, "player-1")
		}
		gotSlotIndex, _ := call.args[1].(int)
		if gotSlotIndex != i {
			t.Errorf("call %d: slot_index %d want %d", i, gotSlotIndex, i)
		}
		gotStartedAtStr, _ := call.args[2].(string)
		gotStartedAt, err := time.Parse(time.RFC3339, gotStartedAtStr)
		if err != nil {
			t.Fatalf("call %d: started_at %q not RFC3339: %v", i, gotStartedAtStr, err)
		}
		wantStartedAt := now.Add(-time.Duration(i) * phase).UTC().Truncate(time.Second)
		if !gotStartedAt.Equal(wantStartedAt) {
			t.Errorf("call %d: started_at %s want %s", i, gotStartedAt, wantStartedAt)
		}
	}
}

func TestPhaseOffset_MatchesIntendedCadence(t *testing.T) {
	// PhaseOffset = interval × maxLayers / slotCount. 30 × 5 / 5 = 30s.
	want := time.Duration(Default.SpawnIntervalSeconds*Default.MaxLayers/Default.SlotCount) * time.Second
	if Default.PhaseOffset() != want {
		t.Errorf("PhaseOffset=%s want %s", Default.PhaseOffset(), want)
	}
}

// --- ApplyGrowth (저장형 성장) ---

func newGrowthDB(t *testing.T) store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cairn.db")
	raw, err := sqlitedb.New(path, Default.SlotCount, int(Default.PhaseOffset().Seconds()))
	if err != nil {
		t.Fatalf("sqlitedb.New: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	return store.NewSQLAdapter(raw)
}

// seedGrowth inserts a player + player_state (with the given cairn_last_growth_at,
// empty = NULL) + slots seeded to the given layer_counts.
func seedGrowth(t *testing.T, db store.DB, playerID string, layers []int, anchor string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `INSERT INTO players (id, steam_id) VALUES (?, ?)`, playerID, "steam-"+playerID); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	if anchor == "" {
		if _, err := db.Exec(ctx, `INSERT INTO player_states (player_id) VALUES (?)`, playerID); err != nil {
			t.Fatalf("seed player_state: %v", err)
		}
	} else {
		if _, err := db.Exec(ctx, `INSERT INTO player_states (player_id, cairn_last_growth_at) VALUES (?, ?)`, playerID, anchor); err != nil {
			t.Fatalf("seed player_state: %v", err)
		}
	}
	nowStr := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	for i, l := range layers {
		if _, err := db.Exec(ctx, `INSERT INTO wish_cairn_slots (player_id, slot_index, started_at, layer_count) VALUES (?, ?, ?, ?)`,
			playerID, i, nowStr, l); err != nil {
			t.Fatalf("seed slot %d: %v", i, err)
		}
	}
}

func readLayers(t *testing.T, db store.DB, playerID string) []int {
	t.Helper()
	rows, err := db.Query(context.Background(),
		`SELECT slot_index, layer_count FROM wish_cairn_slots WHERE player_id = ? ORDER BY slot_index ASC`, playerID)
	if err != nil {
		t.Fatalf("read layers: %v", err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var idx, l int
		if err := rows.Scan(&idx, &l); err != nil {
			t.Fatalf("scan layer: %v", err)
		}
		out = append(out, l)
	}
	return out
}

func readAnchor(t *testing.T, db store.DB, playerID string) (string, bool) {
	t.Helper()
	var a sql.NullString
	if err := db.QueryRow(context.Background(),
		`SELECT cairn_last_growth_at FROM player_states WHERE player_id = ?`, playerID).Scan(&a); err != nil {
		t.Fatalf("read anchor: %v", err)
	}
	return a.String, a.Valid
}

func sumLayers(layers []int) int {
	s := 0
	for _, l := range layers {
		s += l
	}
	return s
}

// (a) 결정적 배분: 같은 (player, anchor, now) 입력 → 두 번 모두 동일한 슬롯별 배분.
func TestApplyGrowth_DeterministicDistribution(t *testing.T) {
	db := newGrowthDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	interval := time.Duration(Default.SpawnIntervalSeconds) * time.Second
	anchor := now.Add(-5 * interval).Format(time.RFC3339)

	seedGrowth(t, db, "p", []int{0, 0, 0, 0, 0}, anchor)

	if err := Default.ApplyGrowthTx(ctx, db, "p", now); err != nil {
		t.Fatalf("ApplyGrowth #1: %v", err)
	}
	first := readLayers(t, db, "p")
	if sumLayers(first) != 5 {
		t.Fatalf("5 steps should add 5 layers, got sum %d (%v)", sumLayers(first), first)
	}

	// reset slots + anchor and replay with the identical now.
	if _, err := db.Exec(ctx, `UPDATE wish_cairn_slots SET layer_count = 0 WHERE player_id = 'p'`); err != nil {
		t.Fatalf("reset slots: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE player_states SET cairn_last_growth_at = ? WHERE player_id = 'p'`, anchor); err != nil {
		t.Fatalf("reset anchor: %v", err)
	}
	if err := Default.ApplyGrowthTx(ctx, db, "p", now); err != nil {
		t.Fatalf("ApplyGrowth #2: %v", err)
	}
	second := readLayers(t, db, "p")

	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("distribution not deterministic: slot %d got %d then %d (first=%v second=%v)", i, first[i], second[i], first, second)
		}
	}
}

// (b) steps 경계: interval 직전=0, interval=1, 나머지 보존.
func TestApplyGrowth_StepsBoundary(t *testing.T) {
	interval := time.Duration(Default.SpawnIntervalSeconds) * time.Second

	cases := []struct {
		name       string
		elapsed    time.Duration
		wantAdded  int
		wantRemain time.Duration // newAnchor 이 now 로부터 얼마나 뒤인가 (나머지)
	}{
		{"exactly-now", 0, 0, 0},
		{"just-before-interval", interval - time.Second, 0, interval - time.Second},
		{"exactly-interval", interval, 1, 0},
		{"interval-plus-remainder", interval + 5*time.Second, 1, 5 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newGrowthDB(t)
			now := time.Now().UTC().Truncate(time.Second)
			anchor := now.Add(-tc.elapsed)
			seedGrowth(t, db, "p", []int{0, 0, 0, 0, 0}, anchor.Format(time.RFC3339))

			if err := Default.ApplyGrowthTx(context.Background(), db, "p", now); err != nil {
				t.Fatalf("ApplyGrowth: %v", err)
			}
			if got := sumLayers(readLayers(t, db, "p")); got != tc.wantAdded {
				t.Errorf("added layers = %d, want %d", got, tc.wantAdded)
			}
			gotAnchor, ok := readAnchor(t, db, "p")
			if !ok {
				t.Fatal("anchor became NULL")
			}
			wantAnchor := now.Add(-tc.wantRemain).Format(time.RFC3339)
			if gotAnchor != wantAnchor {
				t.Errorf("anchor = %q, want %q (remainder preserved)", gotAnchor, wantAnchor)
			}
		})
	}
}

// (c) 전부 완성: 배분 0 이지만 앵커는 steps 만큼 전진(성장 뱅킹 금지).
func TestApplyGrowth_AllComplete_AnchorStillAdvances(t *testing.T) {
	db := newGrowthDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	interval := time.Duration(Default.SpawnIntervalSeconds) * time.Second
	anchor := now.Add(-3 * interval)
	seedGrowth(t, db, "p", []int{5, 5, 5, 5, 5}, anchor.Format(time.RFC3339))

	if err := Default.ApplyGrowthTx(context.Background(), db, "p", now); err != nil {
		t.Fatalf("ApplyGrowth: %v", err)
	}
	if got := sumLayers(readLayers(t, db, "p")); got != 25 {
		t.Errorf("all-complete must add nothing, got sum %d (want 25)", got)
	}
	gotAnchor, _ := readAnchor(t, db, "p")
	if want := now.Format(time.RFC3339); gotAnchor != want {
		t.Errorf("anchor = %q, want %q (must advance by 3×interval even when full)", gotAnchor, want)
	}
}

// (d) NULL 앵커 self-heal: 앵커를 ~now 로 세팅하고 성장 없이 종료.
func TestApplyGrowth_NullAnchorSelfHeal(t *testing.T) {
	db := newGrowthDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	seedGrowth(t, db, "p", []int{0, 0, 0, 0, 0}, "") // NULL anchor

	if err := Default.ApplyGrowthTx(context.Background(), db, "p", now); err != nil {
		t.Fatalf("ApplyGrowth: %v", err)
	}
	if got := sumLayers(readLayers(t, db, "p")); got != 0 {
		t.Errorf("self-heal must not grow, got sum %d", got)
	}
	gotAnchor, ok := readAnchor(t, db, "p")
	if !ok {
		t.Fatal("anchor must be self-healed to non-NULL")
	}
	parsed, err := time.Parse(time.RFC3339, gotAnchor)
	if err != nil {
		t.Fatalf("healed anchor %q not RFC3339: %v", gotAnchor, err)
	}
	if d := time.Since(parsed); d < -5*time.Second || d > 10*time.Second {
		t.Errorf("healed anchor %q not near now (%s off)", gotAnchor, d)
	}
}

// (e) CAS 경합: 앵커 선점(첫 호출이 앵커 전진) 후 두 번째 ApplyGrowth 는 no-op(이중성장 없음).
func TestApplyGrowth_RepeatedCall_NoDoubleGrowth(t *testing.T) {
	db := newGrowthDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	interval := time.Duration(Default.SpawnIntervalSeconds) * time.Second
	anchor := now.Add(-3 * interval)
	seedGrowth(t, db, "p", []int{0, 0, 0, 0, 0}, anchor.Format(time.RFC3339))

	if err := Default.ApplyGrowthTx(context.Background(), db, "p", now); err != nil {
		t.Fatalf("ApplyGrowth #1: %v", err)
	}
	firstSum := sumLayers(readLayers(t, db, "p"))
	if firstSum != 3 {
		t.Fatalf("first call should add 3, got %d", firstSum)
	}
	// same now → anchor already advanced → steps=0 → no-op.
	if err := Default.ApplyGrowthTx(context.Background(), db, "p", now); err != nil {
		t.Fatalf("ApplyGrowth #2: %v", err)
	}
	if secondSum := sumLayers(readLayers(t, db, "p")); secondSum != firstSum {
		t.Errorf("second call double-grew: %d → %d", firstSum, secondSum)
	}
}

// (e-direct) CAS 패자 불변식: CAS affected=0 이면 슬롯 증분을 쓰지 않는다.
func TestApplyGrowth_CASLoser_NoSlotWrite(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	interval := time.Duration(Default.SpawnIntervalSeconds) * time.Second
	exec := &fakeGrowthExec{
		anchorRaw:   now.Add(-3 * interval).Format(time.RFC3339),
		slotRows:    [][2]int{{0, 0}, {1, 0}, {2, 0}, {3, 0}, {4, 0}},
		casAffected: 0, // 다른 요청이 이미 앵커를 선점
	}
	if err := Default.ApplyGrowth(context.Background(), exec, "p", now); err != nil {
		t.Fatalf("ApplyGrowth: %v", err)
	}
	if exec.slotIncrements != 0 {
		t.Errorf("CAS loser must not write slots, got %d slot increments", exec.slotIncrements)
	}
}

// (f) 미완성 소진: steps 가 총 잔여 층수보다 커도 초과 배분 없이 정확히 채운다.
func TestApplyGrowth_IncompleteExhaustion_NoOverCap(t *testing.T) {
	db := newGrowthDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	interval := time.Duration(Default.SpawnIntervalSeconds) * time.Second
	anchor := now.Add(-100 * interval) // steps=100 >> capacity 25
	seedGrowth(t, db, "p", []int{0, 0, 0, 0, 0}, anchor.Format(time.RFC3339))

	if err := Default.ApplyGrowthTx(context.Background(), db, "p", now); err != nil {
		t.Fatalf("ApplyGrowth: %v", err)
	}
	layers := readLayers(t, db, "p")
	for i, l := range layers {
		if l != Default.MaxLayers {
			t.Errorf("slot %d = %d, want exactly MaxLayers %d (no over/under cap)", i, l, Default.MaxLayers)
		}
	}
	// 앵커는 전 구간(100×interval) 전진.
	gotAnchor, _ := readAnchor(t, db, "p")
	if want := now.Format(time.RFC3339); gotAnchor != want {
		t.Errorf("anchor = %q, want %q (advance full steps)", gotAnchor, want)
	}
}

// --- fake growthExecutor for the CAS-loser branch ---

type fakeRow struct{ scan func(dest ...any) error }

func (r fakeRow) Scan(dest ...any) error { return r.scan(dest...) }

type fakeRows struct {
	data [][2]int
	i    int
}

func (r *fakeRows) Next() bool { return r.i < len(r.data) }
func (r *fakeRows) Scan(dest ...any) error {
	*(dest[0].(*int)) = r.data[r.i][0]
	*(dest[1].(*int)) = r.data[r.i][1]
	r.i++
	return nil
}
func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Err() error   { return nil }

type fakeResult struct{ n int64 }

func (r fakeResult) RowsAffected() (int64, error) { return r.n, nil }

type fakeGrowthExec struct {
	anchorRaw      string
	slotRows       [][2]int
	casAffected    int64
	slotIncrements int
}

func (e *fakeGrowthExec) QueryRow(_ context.Context, _ string, _ ...any) store.Row {
	return fakeRow{scan: func(dest ...any) error {
		*(dest[0].(*sql.NullString)) = sql.NullString{String: e.anchorRaw, Valid: e.anchorRaw != ""}
		return nil
	}}
}
func (e *fakeGrowthExec) Query(_ context.Context, _ string, _ ...any) (store.Rows, error) {
	return &fakeRows{data: e.slotRows}, nil
}
func (e *fakeGrowthExec) Exec(_ context.Context, query string, _ ...any) (store.Result, error) {
	if strings.Contains(query, "wish_cairn_slots SET layer_count = layer_count") {
		e.slotIncrements++
		return fakeResult{1}, nil
	}
	if strings.Contains(query, "player_states SET cairn_last_growth_at") {
		return fakeResult{e.casAffected}, nil
	}
	return fakeResult{0}, nil
}
