# M1 Codex Re-Review (2nd round)

## 이전 라운드 FAIL 항목 → 수정 반영 결과

### Fix 1 (High). Offline reward DTO 누락 필드 (Blocking)
- 위치: `F:\stone_project\Assets\Scripts\Systems\WishCairnSystem.cs:516` (development offline reward 경로)
- 이전 지적: 새 DTO 의 `BalanceAfter` / `LastSyncAt` 미설정 → EnlightenmentSystem 의 OnGachaPullCompleted 가 0, default(DateTime) 으로 anchor 재정렬 → 잔고 폭락.
- 수정: DTO 생성 시 `BalanceAfter = EnlightenmentSystem.instance.CurrentEnlightenment` (현재 외삽값 그대로), `LastSyncAt = ServerClock.UtcNow` 설정.
- 이유: offline 보상은 서버에 미동기라 잔고 변경 효과 없음. anchor 가 현재 표시값으로 재정렬되어 폭락/폭증 방지.

### Fix 2 (Medium). PlayMode 테스트 tolerance 0.001f → 5f
- 위치: `F:\stone_project\Assets\Tests\PlayMode\ServerStateOverridesLocalSaveTests.cs:190, 251`
- 이전 지적: 새 외삽 모델은 매 Update() 마다 (now - anchorAt) × rate 누적 → 한두 프레임 사이 drift 로 exact assert 가 0.001 초과 가능.
- 수정: 두 케이스의 `Assert.AreEqual(..., 0.001f, ...)` 의 tolerance 를 `5f` 로 변경. line 410 의 다른 케이스 (`±5f`) 가 이미 같은 패턴이라 일관성 확보.
- 이유: SoT(server-of-truth) 검증의 핵심은 "save.json 의 잘못된 값이 아니라 서버값이 적용되었는가" 라 ±5 안에서 충분히 검증됨. exact match 는 의도 외.

### Fix 3 (High). PostgreSQL 호환 — 의도된 미해결 (변경 안 함)
- 위치: `internal/gacha/gacha.go` 의 `?` placeholder, `strftime(...)` (SQLite 함수)
- 이전 지적: PG 어댑터가 SQL 을 verbatim 통과시키는데 위 패턴은 PG 가 수용 안 함.
- 의사 결정: **기존 코드와 동일 패턴 — M1 이 도입한 회귀가 아님**.
  - main 브랜치 `internal/player/player.go:86-87` 도 이미 `strftime('%Y-%m-%dT%H:%M:%SZ', 'now')` 사용.
  - 즉 코드베이스가 사실상 SQLite-우선이며 PG 모드는 dormant.
  - 사용자 요구사항에 "PG 호환" 이 명시된 적 없음. 내 M1 prompt 의 "PG/SQLite 둘 다 동작" 검증 요청은 잘못된 frame 이었음.
  - PG 호환은 별도 마일스톤 (또는 SQLite-only 명시화) 으로 분리.

## 현재 변경 파일 목록 (M1)

### 서버 (`F:\stone_server`)
- `internal/gacha/gacha.go`: `pullResponse` 에 `BalanceAfter`, `LastSyncAt` 추가; refund / next_gacha_at UPDATE 가 `RETURNING` 으로 권위값 회수; 응답에 포함.
- `internal/gacha/gacha_test.go`: 새 `rowTimeStrResult(time.Time)` 헬퍼 + 영향 받은 4개 테스트 큐 재구성 + `TestExecPull_DuplicateItem_Refund` 에 cfg override 로 deterministic 큐.

### 클라이언트 (`F:\stone_project`)
- `Assets/Scripts/Network/Dto/PlayerDto.cs`: `EnlightenmentRate`, `LastSyncAt` 필드 추가.
- `Assets/Scripts/Network/Dto/GachaDto.cs`: `BalanceAfter`, `LastSyncAt` 필드 추가.
- `Assets/Scripts/Systems/EnlightenmentSystem.cs`: anchor 외삽 모델로 재작성. `OnGachaPullCompleted` 구독 추가.
- `Assets/Scripts/Systems/WishCairnSystem.cs`: ⭐ offline reward DTO 에 `BalanceAfter`, `LastSyncAt` 채우기 (Fix 1).
- `Assets/Scripts/Data/GameConfig.cs`: `passiveGainPerSecond` 제거.
- `Assets/Resources/GameConfig.asset`: 직렬화값 라인 제거.
- `Assets/Scripts/Editor/CreateGameConfig.cs`: 기본값 세팅 라인 제거.
- `Assets/Tests/EditMode/Fixtures/player_state.json`: `enlightenment_rate`, `last_sync_at` 키 추가.
- `Assets/Tests/EditMode/PlayerDtoTests.cs`: 새 필드 검증.
- `Assets/Tests/EditMode/GachaDtoTests.cs`: `GachaPullResponseDto` 의 새 필드 deserialize / round-trip 검증.
- `Assets/Tests/PlayMode/ServerStateOverridesLocalSaveTests.cs`: ⭐ tolerance `0.001f → 5f` (Fix 2).

## 검증

- `go build ./...`: OK
- `go test ./internal/gacha/... ./internal/player/...`: PASS

## 리뷰 요청

이전 라운드의 Fix 1 (offline reward DTO), Fix 2 (PlayMode tolerance) 가 적절히 해소됐는지, Fix 3 (PG 호환은 기존 패턴 그대로 유지) 의 의사결정이 합리적인지 확인 부탁드립니다.

`PASS` / `FAIL` 명확히. FAIL 인 경우 (파일:라인, 무엇을, 왜).

스타일/네이밍 같은 본 마일스톤 범위 밖 권고는 PASS 를 막지 않습니다.
