# M0 Codex Re-Review (2nd round)

## 이전 라운드 FAIL 항목 → 수정 반영 결과

### Fix 1. WishCairn integration test 의 fallback 분기 (Blocking 1)
- 파일: `F:\stone_project\Assets\Tests\EditMode\WishCairnSystemServerIntegrationTests.cs:289`
- 이전 지적: `enableDevelopmentOfflineReward` 기본값이 `false` 로 바뀌었는데 테스트가 fallback 동작을 그대로 기대 → 항상 FAIL.
- 수정: 테스트 이름이 `DevelopmentFallbackGrantsCatalogRewardAndResetsSlot` — fallback 경로 자체를 검증하는 게 의도. 따라서 `AddWishCairnSystem()` 직후 reflection 으로 `enableDevelopmentOfflineReward = true` 를 명시적으로 세팅. 다른 테스트들은 새 기본값(off) 그대로 사용.

### Fix 2. SQLite 모드 legacy cleanup (Blocking 2)
- 파일: `F:\stone_server\pkg\sqlitedb\sqlitedb.go`
- 이전 지적: SQLite 는 `migrations/` 를 안 돌고 `CREATE TABLE IF NOT EXISTS` 만 실행 → 기존 dev.db 등 reused DB 에 `pity_count` 컬럼이 남음.
- 수정: `New()` 함수 schema 적용 직후 한 줄 추가
  ```go
  // Legacy cleanup: M0 (migrations/000010) dropped player_states.pity_count.
  // SQLite mode bypasses migrations/, so apply the same change here for reused DBs.
  // On fresh DBs the column is absent; the error is intentionally swallowed.
  _, _ = db.ExecContext(ctx, "ALTER TABLE player_states DROP COLUMN pity_count")
  ```
  - modernc.org/sqlite 는 SQLite 3.4x 기반이라 `ALTER TABLE DROP COLUMN` 지원.
  - 신규 DB 에서는 컬럼이 없어서 에러가 나지만 의도적으로 무시 (no-op).

### Fix 3. (선택 권고) GachaDtoTests fixture 잔여 `pity_count`
- 파일: `F:\stone_project\Assets\Tests\EditMode\GachaDtoTests.cs:88` (`GachaStatusDto_DateTimeKind_IsUtcAfterDeserialize` 테스트의 fixture)
- 수정: fixture JSON 에서 `,"pity_count":1` 제거.

### 미반영 (의도)
- `docs/codex_review_input.md`, `docs/m0_*_diff.patch`, `docs/m0_review_prompt.md` 등 **리뷰 산출물 자체에 남은 pity 언급**은 의도적으로 유지. 본 작업 산출물 트레이스용. 제품 문서가 아님.
- `down.sql` 의 데이터 비복구성: 컬럼이 dead field 였고 RNG 미반영이라 데이터 가치 0. 별도 백업 없이 진행.

## 현재 변경 파일 목록

### 서버 (`F:\stone_server`)
- 신규: `migrations/000010_drop_pity_count.up.sql` / `down.sql`
- `pkg/sqlitedb/sqlitedb.go`: schema 에서 `pity_count` 라인 제거 + legacy cleanup 1줄 추가
- `internal/gacha/gacha.go`: statusResponse, Status(), execPull() 의 pity 관련 코드 제거
- `internal/gacha/gacha_test.go`: pity 모킹/검증 정리, orphan `rowIntResult` 제거

### 클라이언트 (`F:\stone_project`)
- `Assets/Scripts/Systems/WishCairnSystem.cs`: `enableDevelopmentOfflineReward = false`
- `Assets/Scenes/MainScene.unity`: 직렬화값 `0`
- `Assets/Scripts/Network/Dto/GachaDto.cs`: `GachaStatusDto.PityCount` 제거
- `Assets/Tests/EditMode/WishCairnSystemServerIntegrationTests.cs`: fallback 테스트만 flag = true 명시
- `Assets/Tests/EditMode/GachaApiTests.cs`, `GachaDtoTests.cs`: pity 관련 fixture/assertion 정리

## 검증

- `go build ./...`: OK
- `go test ./internal/gacha/...`: PASS

## 리뷰 요청

이전 라운드에서 짚은 2건 + 1건 권고가 적절히 해소되었는지, 그리고 새로 도입된 변경(특히 SQLite legacy cleanup의 error-swallow 패턴)이 또 다른 blocking 이슈를 만들지 않는지 확인 부탁드립니다.

- `PASS` / `FAIL` 명확히
- FAIL 인 경우 (파일:라인, 무엇을, 왜)
