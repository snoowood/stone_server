# T1-04 Feedback v1

- Reviewed at: `2026-05-03`
- Task: [T1-04.md](/F:/stone_server/Task/T1-04.md)
- Related goal: [02_database.md](/F:/stone_server/Goal/02_database.md)
- Scope: PostgreSQL connection and migrations `000001`-`000002`

## Verdict

통과. T1-04 요구사항 범위에서 확인 가능한 항목들은 충족했습니다.

## Checklist Review

- 서버 시작 시 마이그레이션 자동 적용 로그 출력: 통과
  - 확인 결과: app 시작 로그에서 `running db migrations`, `1/u create_players`, `2/u create_player_states`, `db migrations complete` 출력
- `players`, `player_states` 스키마/제약 조건 일치: 통과
  - `players.id` UUID PK + `gen_random_uuid()`
  - `players.steam_id` UNIQUE NOT NULL
  - `player_states.player_id` PK + `players(id)` FK
  - `player_states.time_stone_count <= 3` 체크 제약 확인
- `down` 마이그레이션 적용 시 테이블 정상 제거: 통과
  - 실제 `migrate down 2` 실행 후 `players`, `player_states` 모두 조회 실패로 제거 확인
- DB 연결 실패 시 서버 시작 중단: 통과
  - 잘못된 `DB_URL`로 실행 시 `migration failed` 로그 후 즉시 종료 확인

## Evidence

- Build check: `go build ./...` 통과
- Fresh isolated compose project boot:
  - `postgres`, `redis`, `app` 기동 성공
  - app 로그에서 자동 마이그레이션 실행 확인
- Schema inspection:
  - [000001_create_players.up.sql](/F:/stone_server/migrations/000001_create_players.up.sql)
  - [000002_create_player_states.up.sql](/F:/stone_server/migrations/000002_create_player_states.up.sql)
  - [db.go](/F:/stone_server/pkg/db/db.go:1)
  - [main.go](/F:/stone_server/cmd/server/main.go:1)
- Down migration check:
  - `migrate/migrate` 컨테이너로 `down 2` 실행
  - 이후 `\d players`, `\d player_states` 모두 not found 확인

## Notes

- 기본 `stone_server` Compose 스택에서는 기존 Postgres 볼륨 상태와 현재 환경값이 어긋난 것으로 보이는 인증 실패가 있었지만, 새 볼륨으로 격리한 초기 기동 검증에서는 정상 통과했습니다.
- 이번 리뷰를 위해 띄운 기본 스택과 격리 스택은 모두 종료했습니다. 격리 스택 볼륨은 삭제했고, 기본 스택 볼륨은 삭제하지 않았습니다.
