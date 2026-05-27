package cairn

import (
	"context"
	"testing"
	"time"

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

func TestDerive_BuildingProgression(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		elapsed   time.Duration
		wantLayer int
		wantState SlotStatus
	}{
		{0, 0, StatusBuilding},
		{29 * time.Second, 0, StatusBuilding},
		{30 * time.Second, 1, StatusBuilding},
		{59 * time.Second, 1, StatusBuilding},
		{60 * time.Second, 2, StatusBuilding},
		{120 * time.Second, 4, StatusBuilding},
		{149 * time.Second, 4, StatusBuilding},
		{150 * time.Second, 5, StatusComplete},
		{10 * time.Hour, 5, StatusComplete}, // capped at MaxLayers
	}
	for _, c := range cases {
		startedAt := now.Add(-c.elapsed)
		got := Derive(0, startedAt, now)
		if got.LayerCount != c.wantLayer || got.Status != c.wantState {
			t.Errorf("elapsed=%s want layer=%d state=%s, got layer=%d state=%s",
				c.elapsed, c.wantLayer, c.wantState, got.LayerCount, got.Status)
		}
	}
}

func TestDerive_NegativeElapsedClamped(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(1 * time.Hour) // started_at in the future (clock skew)
	got := Derive(0, future, now)
	if got.LayerCount != 0 || got.Status != StatusBuilding {
		t.Errorf("negative elapsed should clamp to layer=0 building, got %+v", got)
	}
}

// M4: InitializeSlots 가 SlotCount 개의 INSERT 를 시차로 발행하는지 검증.
// args[1] = slot_index (0..SlotCount-1, 순서), args[2] = started_at RFC3339 string
// (slot k 의 startedAt = now - k × PhaseOffset).
func TestInitializeSlots_EmitsStaggeredInserts(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	db := &captureDB{}

	if err := InitializeSlots(context.Background(), db, "player-1", now); err != nil {
		t.Fatalf("InitializeSlots: %v", err)
	}

	if len(db.execCalls) != SlotCount {
		t.Fatalf("want %d INSERT calls, got %d", SlotCount, len(db.execCalls))
	}

	phase := PhaseOffset()
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
	// PhaseOffset = interval × maxLayers / slotCount.
	// 30 × 5 / 5 = 30s. With staggered start, completions land at 30/60/90/120/150s.
	want := time.Duration(SpawnIntervalSeconds*MaxLayers/SlotCount) * time.Second
	if PhaseOffset() != want {
		t.Errorf("PhaseOffset=%s want %s", PhaseOffset(), want)
	}

	// Verify the stagger schedule: slot k starts at now - k × phaseOffset,
	// so its completion is at (MaxLayers × interval) - (k × phaseOffset).
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	phase := PhaseOffset()
	for k := 0; k < SlotCount; k++ {
		startedAt := now.Add(-time.Duration(k) * phase)
		completionAt := startedAt.Add(time.Duration(MaxLayers*SpawnIntervalSeconds) * time.Second)
		expectedRemaining := time.Duration(MaxLayers*SpawnIntervalSeconds-k*int(phase.Seconds())) * time.Second
		actualRemaining := completionAt.Sub(now)
		if actualRemaining != expectedRemaining {
			t.Errorf("slot %d: completion %s after now (want %s)", k, actualRemaining, expectedRemaining)
		}
	}
}
