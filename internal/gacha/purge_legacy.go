package gacha

import (
	"context"
	"fmt"
	"strings"

	"github.com/gensdeis/stone-server/pkg/store"
)

// legacyItemIDs 는 이전 rng.go 더미 풀의 하드코딩 ID. CSV 마스터 데이터로
// 전환되며 이 ID 들은 새 카탈로그에서 lookup 불가 → 잔여 행 정리 대상.
var legacyItemIDs = []string{
	"common_stone_01", "common_stone_02", "common_stone_03",
	"uncommon_crystal_01", "uncommon_crystal_02",
	"rare_gem_01", "rare_gem_02",
	"unique_relic_01",
	"legendary_timepiece_01",
}

// PurgeLegacyItemIDs 는 inventories / gacha_logs 에 남아있는 legacy 더미 풀
// 행을 삭제한다. 부팅 시 1회 호출(멱등 — 잔여 행이 없으면 no-op).
//
// SQL 마이그레이션이 아닌 코드에서 실행하는 이유:
//   - SQLite 모드는 migrations/ 를 거치지 않으므로 SQL 마이그레이션만으로는
//     SQLite DB 가 mixed-catalog 상태로 남는다.
//   - SkinPool 검증을 통과한 뒤에만 destructive cleanup 을 수행해야, 잘못된
//     CSV 로 부팅 시 데이터 손실을 피할 수 있다.
func PurgeLegacyItemIDs(ctx context.Context, db store.DB) error {
	if len(legacyItemIDs) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(legacyItemIDs))
	placeholders = placeholders[:len(placeholders)-1] // trim trailing ','
	args := make([]any, len(legacyItemIDs))
	for i, id := range legacyItemIDs {
		args[i] = id
	}
	for _, table := range []string{"inventories", "gacha_logs"} {
		q := "DELETE FROM " + table + " WHERE item_id IN (" + placeholders + ")"
		if _, err := db.Exec(ctx, q, args...); err != nil {
			return fmt.Errorf("purge legacy from %s: %w", table, err)
		}
	}
	return nil
}
