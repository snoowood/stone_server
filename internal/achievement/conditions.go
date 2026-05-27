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
	// inventories 는 (player_id, item_id) UNIQUE 라 COUNT(*) 가 "고유 보유 종류 수".
	// 본 조건은 `>= 1` 이라 SUM(count) 와 동작 동일하지만, M3 변경 의도 (집계형 stack) 와는
	// 별개로 "한 종류라도 보유" 의미가 명확해 COUNT(*) 유지.
	var count int
	if err := db.QueryRow(ctx,
		"SELECT COUNT(*) FROM inventories WHERE player_id = ? AND rarity IN ('rare','unique','legendary')",
		playerID,
	).Scan(&count); err != nil {
		return false, err
	}
	return count >= 1, nil
}

func checkLegendary(ctx context.Context, db achievementDB, playerID string) (bool, error) {
	var count int
	if err := db.QueryRow(ctx,
		"SELECT COUNT(*) FROM inventories WHERE player_id = ? AND rarity = 'legendary'",
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
	// "ACH_COLLECTOR = 10 different skins" 기존 의도 보존. inventories 는 (player_id,
	// item_id) UNIQUE 라 COUNT(*) 가 종류 수. M3 의 count stack 은 중복 보유 표현일 뿐
	// collector 정의에는 영향 주지 않도록 COUNT(*) 유지.
	var count int
	if err := db.QueryRow(ctx,
		"SELECT COUNT(*) FROM inventories WHERE player_id = ?",
		playerID,
	).Scan(&count); err != nil {
		return false, err
	}
	return count >= 10, nil
}
