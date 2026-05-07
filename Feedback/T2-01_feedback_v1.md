# T2-01 Feedback v1

- Reviewed at: `2026-05-05`
- Task: [T2-01.md](/F:/stone_server/Task/T2-01.md)
- Related DB spec: [02_database.md](/F:/stone_server/Goal/02_database.md), [05_tasks.md](/F:/stone_server/Goal/05_tasks.md)
- Scope: first review of DB migrations `000003`-`000004`

## Verdict

보류. migration 파일 자체는 저장소에 존재하지만, 실제 앱 기동 후 DB에는 `inventories`와 `gacha_logs`가 생성되지 않았습니다. 현재 상태로는 Phase 2 선행 스키마가 아직 적용되지 않은 것으로 봐야 합니다.

## Findings

1. [P1] 003/004 migration이 실제 DB에 적용되지 않았습니다.
   - 저장소에는 [000003_create_inventories.up.sql](/F:/stone_server/migrations/000003_create_inventories.up.sql) 과 [000004_create_gacha_logs.up.sql](/F:/stone_server/migrations/000004_create_gacha_logs.up.sql) 이 존재하고, embed source에서도 version `1 -> 2 -> 3 -> 4` 순서가 정상 인식됩니다.
   - 그런데 `docker compose up -d --build` 후에도 실제 Postgres의 `schema_migrations` 는 계속 `version = 2` 이고, `public.inventories`, `public.gacha_logs` 테이블이 없습니다.
   - 따라서 이번 태스크의 핵심인 “마이그레이션 자동 적용 확인”이 현재 실패 상태입니다.

## Re-check Summary

- 마이그레이션 자동 적용 확인: 실패
  - 앱 로그에는 `running db migrations` / `db migrations complete` 가 출력되지만
  - 실제 DB 조회 결과 `schema_migrations.version = 2`
  - `inventories`, `gacha_logs` 테이블 없음
- `inventories` UNIQUE `(player_id, item_id)` 제약 확인: 미충족
  - 테이블 자체가 없어 제약 확인 불가
- `gacha_logs` `(player_id, pulled_at DESC)` 인덱스 확인: 미충족
  - 테이블 자체가 없어 인덱스 확인 불가
- 대응 `*.down.sql` 작성: 통과
  - [000003_create_inventories.down.sql](/F:/stone_server/migrations/000003_create_inventories.down.sql)
  - [000004_create_gacha_logs.down.sql](/F:/stone_server/migrations/000004_create_gacha_logs.down.sql)

## Verification

- `docker compose up -d --build` 통과
- 앱 로그 확인
  - `running db migrations`
  - `db migrations complete`
- Postgres 실조회
  - `SELECT * FROM schema_migrations;` -> `version = 2, dirty = false`
  - `information_schema.tables` 조회 -> `inventories`, `gacha_logs` 없음
- embed migration source 확인
  - `EMBED_FILES` 에 `000003`, `000004` 포함
  - `NEXT_FROM_2=3`
  - `NEXT_FROM_3=4`

## Notes

- [dashboard.html](/F:/stone_server/dashboard.html:232) 기본 상태는 현재 `T2-01: 리뷰 중`으로 되어 있어 추가 수정은 하지 않았습니다.
