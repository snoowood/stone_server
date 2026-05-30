// Package cairn contains the server-authoritative WishCairn slot model.
//
// 상태는 (started_at, claimed_at) 두 컬럼만 저장하고, layer_count / status 는
// 매 read 시 서버 시각 기준으로 derive 한다. 클라가 self-report 한 값은
// 검증용으로 받지 않는다.
//
// 슬롯이 동시에 완성되지 않도록 신규 플레이어 슬롯 초기화 시 phase_offset 으로
// 시차를 부여한다 — slot k 의 started_at = now - k × phase_offset.
package cairn

import (
	"context"
	"fmt"
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

// PhaseOffset 은 슬롯 간 시작 시각 차이.
// SpawnInterval × MaxLayers / SlotCount = 30s 마다 한 슬롯씩 완성되는 페이스.
func (c Config) PhaseOffset() time.Duration {
	return time.Duration(c.SpawnIntervalSeconds*c.MaxLayers/c.SlotCount) * time.Second
}

// SlotStatus reflects derived progress of a single slot.
type SlotStatus string

const (
	StatusBuilding SlotStatus = "building"
	StatusComplete SlotStatus = "complete"
)

// SlotState is the derived view of one wish_cairn_slots row at a given instant.
type SlotState struct {
	SlotIndex  int        `json:"slot_index"`
	StartedAt  time.Time  `json:"started_at"`
	LayerCount int        `json:"layer_count"`
	Status     SlotStatus `json:"status"`
}

// Derive computes layer_count and status from started_at and the current instant.
// elapsed < 0 (clock skew) is clamped to 0.
func (c Config) Derive(slotIndex int, startedAt time.Time, now time.Time) SlotState {
	elapsed := now.Sub(startedAt).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	layers := int(elapsed) / c.SpawnIntervalSeconds
	if layers > c.MaxLayers {
		layers = c.MaxLayers
	}
	status := StatusBuilding
	if layers >= c.MaxLayers {
		status = StatusComplete
	}
	return SlotState{
		SlotIndex:  slotIndex,
		StartedAt:  startedAt.UTC(),
		LayerCount: layers,
		Status:     status,
	}
}

// InitializeSlots inserts SlotCount rows for a new player with staggered started_at.
// Idempotent: ON CONFLICT DO NOTHING so re-running on existing rows is safe.
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

// LoadSlots reads all slots for a player as their derived state at `now`.
// Returns rows ordered by slot_index ascending.
func (c Config) LoadSlots(ctx context.Context, db store.DB, playerID string, now time.Time) ([]SlotState, error) {
	rows, err := db.Query(ctx,
		"SELECT slot_index, started_at FROM wish_cairn_slots WHERE player_id = ? ORDER BY slot_index ASC",
		playerID,
	)
	if err != nil {
		return nil, fmt.Errorf("cairn.LoadSlots: %w", err)
	}
	defer rows.Close()

	slots := make([]SlotState, 0, c.SlotCount)
	for rows.Next() {
		var idx int
		var startedAt time.Time
		if err := rows.Scan(&idx, store.ScanTime(&startedAt)); err != nil {
			return nil, fmt.Errorf("cairn.LoadSlots: scan: %w", err)
		}
		slots = append(slots, c.Derive(idx, startedAt, now))
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

// LoadSlotStartedAt returns started_at for a single slot.
// Used by gacha pull (inside the pull transaction) to validate completeness
// before charging the pull cost.
func LoadSlotStartedAt(ctx context.Context, q rowQueryer, playerID string, slotIndex int) (time.Time, error) {
	var startedAt time.Time
	err := q.QueryRow(ctx,
		"SELECT started_at FROM wish_cairn_slots WHERE player_id = ? AND slot_index = ?",
		playerID, slotIndex,
	).Scan(store.ScanTime(&startedAt))
	return startedAt, err
}
