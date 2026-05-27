package achievement

import (
	"context"
	"database/sql"
	"errors"

	"github.com/gensdeis/stone-server/pkg/store"
)

var ErrConditionNotMet = errors.New("condition not met")

type achievementDB interface {
	QueryRow(ctx context.Context, query string, args ...any) store.Row
}

type conditionFn func(ctx context.Context, db achievementDB, playerID string) (bool, error)

const (
	AchFirstPull  = "ACH_FIRST_PULL"
	AchRareUnlock = "ACH_RARE_UNLOCK"
	AchLegendary  = "ACH_LEGENDARY"
	AchStreak7    = "ACH_STREAK_7"
	AchStreak30   = "ACH_STREAK_30"
	AchCollector  = "ACH_COLLECTOR"
)

var registry = map[string]conditionFn{
	AchFirstPull:  checkFirstPull,
	AchRareUnlock: checkRareUnlock,
	AchLegendary:  checkLegendary,
	AchStreak7:    checkStreak7,
	AchStreak30:   checkStreak30,
	AchCollector:  checkCollector,
}

// Check verifies whether a player satisfies the given achievement condition.
func Check(ctx context.Context, db achievementDB, playerID, achievementID string) error {
	fn, ok := registry[achievementID]
	if !ok {
		return ErrConditionNotMet
	}
	met, err := fn(ctx, db, playerID)
	if err != nil {
		return err
	}
	if !met {
		return ErrConditionNotMet
	}
	return nil
}

func checkFirstPull(ctx context.Context, db achievementDB, playerID string) (bool, error) {
	var count int
	if err := db.QueryRow(ctx,
		"SELECT COUNT(*) FROM gacha_logs WHERE player_id = ?",
		playerID,
	).Scan(&count); err != nil {
		return false, err
	}
	return count >= 1, nil
}

func checkRareUnlock(ctx context.Context, db achievementDB, playerID string) (bool, error) {
	// M3: 집계형 inventories — COALESCE(SUM(count), 0) 로 stack 까지 합산.
	// 본 조건은 `>= 1` 이라 행 수와 SUM 결과는 동일하지만, 일관성을 위해 SUM 사용.
	var count int
	if err := db.QueryRow(ctx,
		"SELECT COALESCE(SUM(count), 0) FROM inventories WHERE player_id = ? AND rarity IN ('rare','unique','legendary')",
		playerID,
	).Scan(&count); err != nil {
		return false, err
	}
	return count >= 1, nil
}

func checkLegendary(ctx context.Context, db achievementDB, playerID string) (bool, error) {
	var count int
	if err := db.QueryRow(ctx,
		"SELECT COALESCE(SUM(count), 0) FROM inventories WHERE player_id = ? AND rarity = 'legendary'",
		playerID,
	).Scan(&count); err != nil {
		return false, err
	}
	return count >= 1, nil
}

func checkStreak7(ctx context.Context, db achievementDB, playerID string) (bool, error) {
	return checkStreakDays(ctx, db, playerID, 7)
}

func checkStreak30(ctx context.Context, db achievementDB, playerID string) (bool, error) {
	return checkStreakDays(ctx, db, playerID, 30)
}

func checkStreakDays(ctx context.Context, db achievementDB, playerID string, threshold int) (bool, error) {
	var days int
	err := db.QueryRow(ctx,
		"SELECT streak_days FROM player_states WHERE player_id = ?",
		playerID,
	).Scan(&days)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return days >= threshold, nil
}

func checkCollector(ctx context.Context, db achievementDB, playerID string) (bool, error) {
	// M3: 집계형 inventories — "10 items owned" 의도라 SUM(count) 로 합산 (중복 포함).
	// 만약 "10 종류" 의도라면 COUNT(*) 로 되돌릴 것 — 현 코드는 보유 총량 기준.
	var count int
	if err := db.QueryRow(ctx,
		"SELECT COALESCE(SUM(count), 0) FROM inventories WHERE player_id = ?",
		playerID,
	).Scan(&count); err != nil {
		return false, err
	}
	return count >= 10, nil
}
