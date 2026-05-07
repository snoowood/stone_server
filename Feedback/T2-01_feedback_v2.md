# T2-01 Feedback v2

- Reviewed at: `2026-05-05`
- Task: [T2-01.md](/F:/stone_server/Task/T2-01.md)
- Related DB spec: [02_database.md](/F:/stone_server/Goal/02_database.md), [05_tasks.md](/F:/stone_server/Goal/05_tasks.md)
- Scope: second review of DB migrations `000003`-`000004` after feedback reflection

## Verdict

통과. 지난 리뷰에서 보였던 runtime 미적용 상태는 재현되지 않았고, 003/004 migration이 실제 DB에 정상 반영된 것을 확인했습니다.

## Re-check Summary

- 마이그레이션 자동 적용 확인: 통과
  - 앱 로그에서 `3/u create_inventories`, `4/u create_gacha_logs` 실행 확인
  - `schema_migrations.version = 4`, `dirty = false`
- `inventories` UNIQUE `(player_id, item_id)` 제약 확인: 통과
  - `inventories_player_id_item_id_key` 존재 확인
- `gacha_logs` `(player_id, pulled_at DESC)` 인덱스 확인: 통과
  - `idx_gacha_logs_player_pulled_at` 존재 확인
- 대응 `*.down.sql` 작성: 통과

## Verification

- `go test ./...` 통과
- `docker compose up -d --build` 통과
- 앱 로그 확인
  - `migrate: 3/u create_inventories`
  - `migrate: 4/u create_gacha_logs`
- Postgres 실조회
  - `schema_migrations` -> `version = 4`, `dirty = false`
  - `public.inventories`, `public.gacha_logs` 테이블 존재
  - `inventories_player_id_item_id_key -> UNIQUE (player_id, item_id)`
  - `idx_gacha_logs_player_pulled_at -> (player_id, pulled_at DESC)`

## Notes

- 이번 2차 리뷰에서는 추가 findings가 없습니다.
