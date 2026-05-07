package achievement

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/gensdeis/stone-server/pkg/store"
)

// mockRow implements store.Row via a scanFn closure.
type mockRow struct {
	scanFn func(dest ...any) error
}

func (r mockRow) Scan(dest ...any) error { return r.scanFn(dest...) }

// mockDB implements achievementDB with a single queryRowFn.
type mockDB struct {
	queryRowFn func(ctx context.Context, query string, args ...any) store.Row
}

func (m *mockDB) QueryRow(ctx context.Context, query string, args ...any) store.Row {
	return m.queryRowFn(ctx, query, args...)
}

// rowInt returns a mockRow that scans the given int into the first dest.
func rowInt(n int) mockRow {
	return mockRow{scanFn: func(dest ...any) error {
		*dest[0].(*int) = n
		return nil
	}}
}

// rowErr returns a mockRow whose Scan returns err.
func rowErr(err error) mockRow {
	return mockRow{scanFn: func(dest ...any) error { return err }}
}

var ctx = context.Background()

// --- ACH_FIRST_PULL ---

func TestCheckFirstPull_Met(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(1) }}
	if err := Check(ctx, db, "p1", AchFirstPull); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheckFirstPull_NotMet(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(0) }}
	if !errors.Is(Check(ctx, db, "p1", AchFirstPull), ErrConditionNotMet) {
		t.Fatal("expected ErrConditionNotMet")
	}
}

func TestCheckFirstPull_DBError(t *testing.T) {
	dbErr := errors.New("db down")
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowErr(dbErr) }}
	if !errors.Is(Check(ctx, db, "p1", AchFirstPull), dbErr) {
		t.Fatal("expected db error")
	}
}

// --- ACH_RARE_UNLOCK ---

func TestCheckRareUnlock_Met(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(2) }}
	if err := Check(ctx, db, "p1", AchRareUnlock); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheckRareUnlock_NotMet(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(0) }}
	if !errors.Is(Check(ctx, db, "p1", AchRareUnlock), ErrConditionNotMet) {
		t.Fatal("expected ErrConditionNotMet")
	}
}

// --- ACH_LEGENDARY ---

func TestCheckLegendary_Met(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(1) }}
	if err := Check(ctx, db, "p1", AchLegendary); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheckLegendary_NotMet(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(0) }}
	if !errors.Is(Check(ctx, db, "p1", AchLegendary), ErrConditionNotMet) {
		t.Fatal("expected ErrConditionNotMet")
	}
}

// --- ACH_STREAK_7 ---

func TestCheckStreak7_Met(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(7) }}
	if err := Check(ctx, db, "p1", AchStreak7); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheckStreak7_NotMet(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(6) }}
	if !errors.Is(Check(ctx, db, "p1", AchStreak7), ErrConditionNotMet) {
		t.Fatal("expected ErrConditionNotMet")
	}
}

func TestCheckStreak7_NoRows(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowErr(sql.ErrNoRows) }}
	if !errors.Is(Check(ctx, db, "p1", AchStreak7), ErrConditionNotMet) {
		t.Fatal("expected ErrConditionNotMet on no rows")
	}
}

// --- ACH_STREAK_30 ---

func TestCheckStreak30_Met(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(30) }}
	if err := Check(ctx, db, "p1", AchStreak30); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheckStreak30_NotMet(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(29) }}
	if !errors.Is(Check(ctx, db, "p1", AchStreak30), ErrConditionNotMet) {
		t.Fatal("expected ErrConditionNotMet")
	}
}

// --- ACH_COLLECTOR ---

func TestCheckCollector_Met(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(10) }}
	if err := Check(ctx, db, "p1", AchCollector); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheckCollector_NotMet(t *testing.T) {
	db := &mockDB{queryRowFn: func(_ context.Context, _ string, _ ...any) store.Row { return rowInt(9) }}
	if !errors.Is(Check(ctx, db, "p1", AchCollector), ErrConditionNotMet) {
		t.Fatal("expected ErrConditionNotMet")
	}
}

// --- unknown achievement ID ---

func TestCheck_UnknownID(t *testing.T) {
	db := &mockDB{}
	if !errors.Is(Check(ctx, db, "p1", "ACH_DOES_NOT_EXIST"), ErrConditionNotMet) {
		t.Fatal("expected ErrConditionNotMet for unknown ID")
	}
}
