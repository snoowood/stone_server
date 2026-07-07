// Package cairn contains the server-authoritative WishCairn slot model.
//
// 저장형(stateful) 모델(issue #34): 각 슬롯의 layer_count 를 실제로 저장한다. 성장은
// 접속 중에만 일어나며(오프라인 미성장), interval(SpawnIntervalSeconds)마다 미완성 슬롯
// 중 결정적 무작위 1곳이 +1층 한다. player_states.cairn_last_growth_at 이 성장 앵커이고,
// ApplyGrowth 가 요청 편승(GET /player/state, POST /player/sync, gacha pull) 시 lazy 로
// anchor→now 구간을 적립한다. auth 는 세션 시작 시 앵커를 now 로 리셋해 오프라인 구간을
// 폐기한다(internal/auth resetAccrualAnchor).
//
// 과거 모델은 layer = (now - started_at)/interval 로 매 read 시 derive 했으나, 랜덤 성장 +
// 오프라인 미성장과 양립 불가라 폐기했다. started_at 은 표시/이력용 vestige 로만 남는다.
package cairn

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/gensdeis/stone-server/pkg/store"
)

// Config holds WishCairn balance dimensions. Previously compile-time consts;
// now injected from the shared game-config.xml (pkg/gameconfig) at boot.
type Config struct {
	SlotCount            int
	MaxLayers            int
	SpawnIntervalSeconds int
}

// Default mirrors the historical const values (5/5/30) for tests and reference.
// It is NOT an automatic runtime fallback: production always injects the
// XML-derived Config, and an invalid/absent XML fails the boot (pkg/gameconfig),
// so a zero-value Config never reaches a handler.
var Default = Config{SlotCount: 5, MaxLayers: 5, SpawnIntervalSeconds: 30}

// PhaseOffset 은 신규 슬롯 초기화 시 started_at 시차. 저장형 모델에서 started_at 은 더는
// 성장에 관여하지 않으므로(표시/이력용 vestige) 이 값도 stagger 시딩 용도로만 남는다.
// SpawnInterval × MaxLayers / SlotCount 의 역사적 페이스를 그대로 유지한다.
func (c Config) PhaseOffset() time.Duration {
	return time.Duration(c.SpawnIntervalSeconds*c.MaxLayers/c.SlotCount) * time.Second
}

// SlotStatus reflects the progress of a single slot.
type SlotStatus string

const (
	StatusBuilding SlotStatus = "building"
	StatusComplete SlotStatus = "complete"
)

// SlotState is the view of one wish_cairn_slots row. LayerCount 는 저장값 그대로이고,
// Status 는 layer_count 로부터 파생한다(더는 시간 derive 가 아니다). JSON 필드는 클라
// 계약 유지를 위해 그대로 둔다.
type SlotState struct {
	SlotIndex  int        `json:"slot_index"`
	StartedAt  time.Time  `json:"started_at"`
	LayerCount int        `json:"layer_count"`
	Status     SlotStatus `json:"status"`
}

// statusFor 는 저장된 layer_count 로부터 표시용 status 를 파생한다.
func (c Config) statusFor(layerCount int) SlotStatus {
	if layerCount >= c.MaxLayers {
		return StatusComplete
	}
	return StatusBuilding
}

// InitializeSlots inserts SlotCount rows for a new player with staggered started_at.
// Idempotent: ON CONFLICT DO NOTHING so re-running on existing rows is safe.
// 저장형 전환 후 started_at 은 성장에 관여하지 않는다(layer_count 는 DEFAULT 0 으로 시작).
// stagger 는 표시용 vestige 로만 남긴다 — sqlitedb.New 호출부 시그니처 안정을 위해 유지.
func (c Config) InitializeSlots(ctx context.Context, db store.DB, playerID string, now time.Time) error {
	phase := c.PhaseOffset()
	for i := 0; i < c.SlotCount; i++ {
		startedAt := now.Add(-time.Duration(i) * phase).UTC().Truncate(time.Second)
		_, err := db.Exec(ctx,
			"INSERT INTO wish_cairn_slots (player_id, slot_index, started_at) VALUES (?, ?, ?) ON CONFLICT (player_id, slot_index) DO NOTHING",
			playerID, i, startedAt.Format(time.RFC3339),
		)
		if err != nil {
			return fmt.Errorf("cairn.InitializeSlots: slot %d: %w", i, err)
		}
	}
	return nil
}

// LoadSlots reads all stored slots for a player, ordered by slot_index ascending.
func (c Config) LoadSlots(ctx context.Context, db store.DB, playerID string) ([]SlotState, error) {
	rows, err := db.Query(ctx,
		"SELECT slot_index, started_at, layer_count FROM wish_cairn_slots WHERE player_id = ? ORDER BY slot_index ASC",
		playerID,
	)
	if err != nil {
		return nil, fmt.Errorf("cairn.LoadSlots: %w", err)
	}
	defer rows.Close()

	slots := make([]SlotState, 0, c.SlotCount)
	for rows.Next() {
		var idx, layerCount int
		var startedAt time.Time
		if err := rows.Scan(&idx, store.ScanTime(&startedAt), &layerCount); err != nil {
			return nil, fmt.Errorf("cairn.LoadSlots: scan: %w", err)
		}
		slots = append(slots, SlotState{
			SlotIndex:  idx,
			StartedAt:  startedAt.UTC(),
			LayerCount: layerCount,
			Status:     c.statusFor(layerCount),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cairn.LoadSlots: iterate: %w", err)
	}
	return slots, nil
}

// rowQueryer is satisfied by both store.DB and store.Tx so the same code path
// works inside or outside a transaction.
type rowQueryer interface {
	QueryRow(ctx context.Context, query string, args ...any) store.Row
}

// LoadSlotLayerCount returns the stored layer_count for a single slot. gacha pull uses
// it (inside the pull tx) to tell CAIRN_SLOT_NOT_FOUND (sql.ErrNoRows) apart from
// CAIRN_INCOMPLETE after the completeness CAS misses (affected=0).
func LoadSlotLayerCount(ctx context.Context, q rowQueryer, playerID string, slotIndex int) (int, error) {
	var layerCount int
	err := q.QueryRow(ctx,
		"SELECT layer_count FROM wish_cairn_slots WHERE player_id = ? AND slot_index = ?",
		playerID, slotIndex,
	).Scan(&layerCount)
	return layerCount, err
}

// growthExecutor 는 ApplyGrowth 가 필요로 하는 최소 DB 표면이다. store.DB 와 store.Tx
// 둘 다 만족하므로 gacha pull tx 안에서도, player 핸들러가 tx 로 감싼 경우에도 재사용된다.
type growthExecutor interface {
	QueryRow(ctx context.Context, query string, args ...any) store.Row
	Query(ctx context.Context, query string, args ...any) (store.Rows, error)
	Exec(ctx context.Context, query string, args ...any) (store.Result, error)
}

// growthSeed 는 한 성장 step 의 결정적 시드다. 입력이 playerID 와 그 step 의 절대 시각
// (unix 초)이라 서버가 재현·감사 가능하고, 부분 적용 후 재계산해도 이미 지난 step 시드가
// 불변이다(같은 (player, anchor, now) → 항상 같은 배분).
func growthSeed(playerID string, stepUnix int64) uint64 {
	h := fnv.New64a()
	h.Write([]byte(playerID + ":" + strconv.FormatInt(stepUnix, 10)))
	return h.Sum64()
}

// ApplyGrowth lazily advances stored WishCairn growth for one player up to `now`.
// exec 는 store.Tx(gacha pull) 또는 tx 로 감싼 store.DB(player 핸들러, ApplyGrowthTx) 다.
//
// 동시성: cairn_last_growth_at 에 대한 CAS(①에서 읽은 원본 TEXT 로 비교)가 게이트다.
// 한 성장 구간은 오직 CAS 승자만 슬롯을 증분한다 — affected=0 이면 다른 요청이 이미 그
// 구간을 적립한 것이므로 슬롯을 건드리지 않고 조용히 종료(이중 성장 방지). SQLite
// MaxOpenConns=1 은 tx 가 단일 쓰기 커넥션을 점유하게 해 read→CAS→write 를 원자화한다.
//
// SQL 은 SQLite 우선(strftime/? 바인딩). PG path 는 R1(B) 전까지 비동작.
func (c Config) ApplyGrowth(ctx context.Context, exec growthExecutor, playerID string, now time.Time) error {
	now = now.UTC()

	// ① 앵커 원본 TEXT + 파싱값. CAS 비교는 반드시 이 원본 TEXT 로 한다(포맷 드리프트 방지).
	var rawAnchor sql.NullString
	if err := exec.QueryRow(ctx,
		"SELECT cairn_last_growth_at FROM player_states WHERE player_id = ?", playerID,
	).Scan(&rawAnchor); err != nil {
		return fmt.Errorf("cairn.ApplyGrowth: read anchor: %w", err)
	}

	// ② NULL 앵커 self-heal: 마이그레이션 직후 활성 세션(auth 리셋 전)은 앵커가 NULL 이다.
	//    now 로 self-heal 하고 이번엔 성장 없이(steps=0) 종료 — 다음 요청부터 정상 적립.
	if !rawAnchor.Valid {
		if _, err := exec.Exec(ctx,
			"UPDATE player_states SET cairn_last_growth_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE player_id = ? AND cairn_last_growth_at IS NULL",
			playerID,
		); err != nil {
			return fmt.Errorf("cairn.ApplyGrowth: self-heal null anchor: %w", err)
		}
		return nil
	}

	anchor, err := time.Parse(time.RFC3339, rawAnchor.String)
	if err != nil {
		return fmt.Errorf("cairn.ApplyGrowth: parse anchor %q: %w", rawAnchor.String, err)
	}
	anchor = anchor.UTC()

	// ③ steps = floor((now - anchor)/interval). <=0 이면(동일 시각·clock skew) no-op.
	interval := time.Duration(c.SpawnIntervalSeconds) * time.Second
	if interval <= 0 {
		return fmt.Errorf("cairn.ApplyGrowth: invalid interval %ds", c.SpawnIntervalSeconds)
	}
	steps := int(now.Sub(anchor) / interval)
	if steps <= 0 {
		return nil
	}

	// ④ 슬롯 layer_count 로드 (slot_index 오름차순).
	rows, err := exec.Query(ctx,
		"SELECT slot_index, layer_count FROM wish_cairn_slots WHERE player_id = ? ORDER BY slot_index ASC",
		playerID,
	)
	if err != nil {
		return fmt.Errorf("cairn.ApplyGrowth: load slots: %w", err)
	}
	type slotLayer struct {
		idx   int
		layer int
	}
	var slots []slotLayer
	for rows.Next() {
		var s slotLayer
		if err := rows.Scan(&s.idx, &s.layer); err != nil {
			rows.Close()
			return fmt.Errorf("cairn.ApplyGrowth: scan slot: %w", err)
		}
		slots = append(slots, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("cairn.ApplyGrowth: iterate slots: %w", err)
	}
	rows.Close()

	// ④b 슬롯이 아직 SlotCount 만큼 초기화되지 않았으면(미/부분 초기화 — GetState 의
	//    lazy-init 전에 sync 가 먼저 온 경우 등) 성장을 보류한다. 여기서 앵커를 전진시키면
	//    배분 대상이 없어 그 구간의 성장이 조용히 소실되므로, 앵커를 건드리지 않고 종료해
	//    슬롯 보강 후 같은 윈도우가 동일 시드로 재적립되게 한다 (codex 리뷰 지적).
	//    전 슬롯 완성(아래 incomplete 소진)과는 다른 상태다 — 그쪽은 앵커를 전진시켜야 한다.
	if len(slots) < c.SlotCount {
		return nil
	}

	// ⑤ step i(1..steps)마다 미완성 슬롯(slot_index 오름차순) 중 결정적 무작위 1곳을 +1.
	//    미완성이 비면(전 슬롯 완성) 배분을 중단한다 — 그래도 앵커는 아래 ⑥에서 steps 만큼
	//    전진한다(성장 뱅킹 금지). applied[j] 는 slots[j] 에 최종적으로 더할 층수.
	applied := make([]int, len(slots))
	incomplete := make([]int, 0, len(slots))
	for i := 1; i <= steps; i++ {
		incomplete = incomplete[:0]
		for j := range slots {
			if slots[j].layer+applied[j] < c.MaxLayers {
				incomplete = append(incomplete, j)
			}
		}
		if len(incomplete) == 0 {
			break
		}
		stepUnix := anchor.Add(time.Duration(i) * interval).Unix()
		pos := incomplete[growthSeed(playerID, stepUnix)%uint64(len(incomplete))]
		applied[pos]++
	}

	// ⑥ newAnchor = anchor + steps×interval (나머지 보존, RFC3339 초단위 UTC). 전 슬롯 완성
	//    상태여도 항상 steps 만큼 전진해 pull 직후 백로그가 한꺼번에 터지는 것을 막는다.
	newAnchor := anchor.Add(time.Duration(steps) * interval).UTC().Truncate(time.Second).Format(time.RFC3339)

	// ⑦ CAS 로 앵커 전진. ①에서 읽은 원본 TEXT 로 비교.
	casRes, err := exec.Exec(ctx,
		"UPDATE player_states SET cairn_last_growth_at = ? WHERE player_id = ? AND cairn_last_growth_at = ?",
		newAnchor, playerID, rawAnchor.String,
	)
	if err != nil {
		return fmt.Errorf("cairn.ApplyGrowth: cas anchor: %w", err)
	}
	if affected, _ := casRes.RowsAffected(); affected == 0 {
		// 동시 요청이 이미 이 구간을 적립함 — 슬롯을 쓰지 않고 조용히 종료(이중 성장 방지).
		return nil
	}

	// CAS 승자만 여기 도달. 변경된 슬롯만 증분한다.
	for j := range slots {
		if applied[j] == 0 {
			continue
		}
		if _, err := exec.Exec(ctx,
			"UPDATE wish_cairn_slots SET layer_count = layer_count + ? WHERE player_id = ? AND slot_index = ?",
			applied[j], playerID, slots[j].idx,
		); err != nil {
			return fmt.Errorf("cairn.ApplyGrowth: increment slot %d: %w", slots[j].idx, err)
		}
	}
	return nil
}

// ApplyGrowthTx runs ApplyGrowth inside a fresh transaction for callers that lack one
// (the player handlers). The gacha pull path already has a tx and calls ApplyGrowth
// directly. Wrapping in a tx holds SQLite's single write connection (MaxOpenConns=1)
// for the whole read→CAS→write sequence, so a concurrent same-player growth cannot
// observe a half-applied window and over-increment a slot past MaxLayers.
func (c Config) ApplyGrowthTx(ctx context.Context, db store.DB, playerID string, now time.Time) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := c.ApplyGrowth(ctx, tx, playerID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
