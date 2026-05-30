# 주간 작업 계획서 — 가챠 패킷 검증 · 공통 설정 XML화 · N Power 동기화

- 작성일: 2026-05-31
- 작성: Claude (설계/구현 담당)
- 리뷰: Codex (검증/리뷰/수정 담당)
- 대상 레포: `stone_server`(Go 백엔드), `stone_project`(Unity 클라이언트)

---

## 0. 배경 및 사전 조사 결과

이번 주 원본 작업 리스트는 다음 4가지였다.

1. 가챠 클라이언트 → 서버 요청 패킷 구현
2. 가챠 성공/실패 결과값 서버 → 클라이언트 반환 패킷 구현
3. 클라이언트 `GameConfig.cs` 내용을 서버와 공통으로 쓰기 위해 XML로 공통화 + 양쪽 캐싱
4. N Power(=Enlightenment) 값이 클라(초당 1 증가)와 서버가 동일한지 sync 맞추는 패킷 추가

**사전 조사로 확인된 코드베이스 실태 (중요):**

- **작업 1·2는 이미 구현되어 있다.**
  - 요청: `POST /api/v1/gacha/pull` ([internal/gacha/gacha.go:92](../../internal/gacha/gacha.go)) ↔ 클라 `GachaApi.PullAsync(slotIndex)`
  - 응답: `GachaPullResponseDto` (item_id, rarity, is_duplicate, refund_points, next_gacha_at, balance_after, last_sync_at, new_count) 전부 왕복 처리됨
  - 단, 트리거가 WishCairn 슬롯 완성 → claim(`slot_index` 기반)에 묶여 있음
  - 에러 처리: 409 INSUFFICIENT_POINTS / 429 COOLDOWN_ACTIVE / 403 CAIRN_INCOMPLETE 분기 존재
- **작업 4도 부분 존재한다.**
  - 클라: `EnlightenmentSystem.anchorRate=1.0` 초당 외삽 (`currentEnlightenment = anchorPts + elapsed * anchorRate`)
  - 서버: `enlightenment_rate FLOAT8 DEFAULT 1.0`(migration 000009) + `POST /api/v1/player/sync`로 경과시간 × rate 누적
  - ⚠️ **(Codex 정정)** `/player/sync` 응답(`syncResponse`, [player.go:56](../../internal/player/player.go))에는 `enlightenment_rate`가 **없다**. rate는 `/player/state`(`stateResponse`, [player.go:46](../../internal/player/player.go))에서만 내려간다. → 작업 4의 "rate 동기화" 경로는 sync가 아니라 state 응답이다.
  - "rate가 동일한지 검증하는 별도 패킷"은 없음

**의사결정 (사용자 확정):**

| 작업 | 확정 범위 |
|---|---|
| 1·2 가챠 패킷 | **기존 흐름 검증/보완** — 신규 엔드포인트 없음. 누락 필드·에러 처리·재시도 점검 |
| 3 공통 설정 | **단일 소스 + 동기화** — `stone_project`를 원본으로 두고 `skins.csv` 선례처럼 `stone_server`로 XML 복사 |
| 4 N Power sync | **기존 `/player/sync`로 충분** — 신규 패킷 없이 rate까지 동기화되는지 검증 중심 |

→ 결과적으로 이번 주 **핵심 신규 작업은 3(공통 설정 XML화)** 하나이고, 1·2·4는 검증/보완 작업이다.

---

## 1. 공통 설정값 인벤토리 (작업 3의 대상)

현재 동일 의미의 값이 클라/서버에 **각각 하드코딩**되어 단일 소스가 없다.

| 논리 키 | 클라이언트 (`Assets/Scripts/Data/GameConfig.cs`) | 서버 | 공통화 대상 |
|---|---|---|---|
| 가챠 비용 | `wishCairnCost = 100` | `internal/gacha/rng.go:46` `PullCost: 100` | ✅ |
| 가챠 쿨다운(분) | `wishCairnCooldownMinutes = 30` | `internal/gacha/gacha.go:22` `cooldownTTL = 30m` | ✅ |
| 돌 레이어 생성 간격(초) | `wishCairnStoneSpawnIntervalSeconds = 30` | `internal/cairn/cairn.go:24` `SpawnIntervalSeconds = 30` | ✅ |
| 슬롯 수 | `wishCairnSlotCount = 5` | `internal/cairn/cairn.go:22` `SlotCount = 5` | ✅ |
| 최대 레이어 수 | `wishCairnMaxLayerCount = 5` | `internal/cairn/cairn.go:23` `MaxLayers = 5` | ✅ |
| Enlightenment 증가율(초당) | `EnlightenmentSystem.anchorRate = 1.0` | migration 000009 `enlightenment_rate DEFAULT 1.0` | ⚠️ 검토 (작업 4와 연계) |
| 입력당 포인트 | `pointsPerInput = 1` | (서버에 없음, 클라 전용) | ❌ 클라 전용 |
| 수집 표시 비용 | `wishCairnCollectCost = 1000` | (서버 권위, 표시용) | ❌ 표시 전용 |
| 랜덤 이벤트 간격 | `eventMinInterval=120 / eventMaxInterval=300` | (서버에 없음) | ❌ 클라 전용 |
| 최대 타임스톤 | `maxTimeStones = 3` | `migrations/000002` + `sqlitedb.go` `time_stone_count <= 3` 제약 | ❌ 이미 일치(DB 제약) |

**공통화 1차 대상(✅)은 5개**: 가챠 비용, 쿨다운, 돌 생성 간격, 슬롯 수, 최대 레이어 수. 이 5개는 클라/서버가 반드시 동일해야 게임 로직 정합성이 유지된다.

> ⚠️ 서버의 cairn 상수(`SlotCount`, `MaxLayers`, `SpawnIntervalSeconds`)는 현재 컴파일타임 `const`이며 `PhaseOffset()`/`Derive()`의 정수 나눗셈에 직접 쓰인다(`SpawnIntervalSeconds*MaxLayers/SlotCount`). 런타임 설정으로 전환 시 이 함수들의 시그니처/주입 방식 변경이 필요하다 — 작업 3-S3의 핵심 난이도.

> ⚠️ **(Codex 정정) 같은 5/30 값이 DB bootstrap에도 하드코딩되어 있다** — `internal/*`만 바꾸면 XML과 DB 초기화가 어긋난다:
> - `migrations/000011_create_wish_cairn_slots.up.sql:12~19` — 기존 player backfill에 `5 슬롯`, `INTERVAL '30 seconds'` 하드코딩
> - `pkg/sqlitedb/sqlitedb.go:150,153` — SQLite 모드 슬롯 시드에 `s.idx * 30`, `SELECT 0..4` 하드코딩
> 마이그레이션은 과거 시점 스냅샷이라 보통 소급 변경하지 않으므로, **신규 슬롯 시드 경로(SQLite seed + 신규 player 초기화)만 config 기반으로 전환**하고 과거 마이그레이션은 그대로 두는 정책이 현실적이다(작업 3-S4에서 결정).

---

## 2. 세분화 작업 항목

각 항목은 `작업 → 검증 기준(verify)` 형태로 정의한다.

### 작업 1·2 — 가챠 요청/응답 패킷 검증·보완

- **T1-1. 기존 pull 왕복 흐름 정밀 점검**
  - 서버 `pullRequest`/`pullResponse` 필드와 클라 `GachaPullResponseDto` 필드 1:1 매핑표 작성, snake_case 직렬화 누락 확인
  - verify: 필드 매핑표에 누락/타입 불일치 0건. 실제 pull 1회 e2e 응답 JSON과 DTO 역직렬화 성공 확인
- **T1-2. 실패 경로 응답 규약 점검**
  - 409/429/403 각 케이스의 응답 바디(error code, message) 구조가 클라 분기(`OnInsufficientPoints`, status 재조회, 레이어 롤백)와 일치하는지 확인
  - verify: 3개 에러 케이스 각각 서버 응답 → 클라 분기 동작 일치. 불일치 시 보완 목록 도출
- **T1-3. 재시도/멱등성 점검**
  - `/gacha/pull`이 의도적으로 OfflineQueue 비대상인 점, 중복 제출 방지(쿨다운/티켓) 동작 확인
  - verify: 동일 요청 2회 연속 시 중복 차감 없음(쿨다운으로 2번째 429) 테스트 통과

### 작업 3 — 공통 설정 XML화 + 양쪽 캐싱 (핵심)

- **T3-S1. XML 스키마 설계 (단일 소스)**
  - `stone_project`에 원본 XML 정의(예: `Assets/Resources/game-config.xml` → 빌드시 TextAsset). 스키마: 키-값 + `version` 속성
  - 1차 대상 5개 키 + `version` 포함. 클라 전용 값은 XML에 넣지 않고 기존 위치 유지(범위 한정)
  - verify: XML 스키마 문서 + 예시 파일 작성. 양쪽이 합의한 키 네이밍/단위(초/분) 확정
- **T3-S2. 클라이언트 XML 로더/캐시**
  - `GameConfigLoader`: 부팅 시 XML(TextAsset) 파싱 → 런타임 캐시. 기존 `GameConfig.asset` 의존부를 캐시 참조로 전환(또는 캐시→ScriptableObject 주입)
  - XML 누락/파싱 실패 시 기존 기본값 폴백
  - verify: 에디터 플레이에서 XML 값이 `wishCairnCost` 등에 반영됨 확인. 손상 XML 시 폴백 + 경고 로그
- **T3-S3. 서버 XML 로더/캐시 + 하드코딩 제거** — **난이도: 상 (Codex 상향)**
  - `pkg/gameconfig` 신설: 부팅 시 `Data/game-config.xml` 파싱 → 캐시(불변, 동시 읽기 안전). `skins.csv` 로딩 패턴(fail-fast) 답습
  - `gacha.PullCost`, `cooldownTTL`, `cairn.SlotCount/MaxLayers/SpawnIntervalSeconds`를 캐시 주입으로 전환
  - ⚠️ cairn `const` → 런타임 전환: `PhaseOffset()`/`Derive()`/`InitializeSlots()` 시그니처에 config 주입. 0 나눗셈/경계 회귀 주의
  - ⚠️ **(Codex) 주입 연쇄 변경 범위**: cairn 함수 시그니처뿐 아니라 생성자/주입 경로가 연쇄로 바뀐다 — `internal/auth/handler.go:154`, `internal/player/player.go:181`(cairn 사용부), `internal/gacha/gacha.go:33`, `cmd/server/main.go:144`
  - ⚠️ **(Codex) config validation 필수**: `slot_count > 0`, `max_layers > 0`, `spawn_interval > 0`, `PhaseOffset() > 0`([cairn.go:29](../../internal/cairn/cairn.go)) 검사를 로더에 추가(부팅 fail-fast)
  - verify: 기존 cairn/gacha 단위테스트 전부 통과(`go test ./internal/cairn ./internal/gacha`). XML 미존재/스키마 오류/0 이하 값 시 부팅 fail-fast
- **T3-S3b. DB bootstrap 경로 config화 + 기존 슬롯 데이터 정책** — **(Codex 신규)**
  - SQLite 시드(`pkg/sqlitedb/sqlitedb.go:150,153`)의 `s.idx*30`/`SELECT 0..4`를 config 기반 생성으로 전환. PostgreSQL은 신규 player 초기화([player.go:168](../../internal/player/player.go), `InitializeSlots`)가 config 기반이면 충분
  - ⚠️ **기존 저장 데이터 처리 정책: 마이그레이션 (R5)**: [cairn.go:91](../../internal/cairn/cairn.go)은 모든 슬롯 row를 읽고 [player.go:168](../../internal/player/player.go)은 부족분만 INSERT한다. → `SlotCount`를 **줄이면** 예전 잉여 row가 응답에 섞인다. **신규 마이그레이션으로 `slot_index >= SlotCount` row를 DELETE**(런타임 필터링이 아니라 DB 정리). 늘릴 때는 신규 player 초기화/state 보충 경로가 부족분 INSERT 처리
  - 과거 마이그레이션(`000011`)은 소급 변경하지 않음(스냅샷). 정리는 **신규 마이그레이션 파일**로 추가
  - verify: SlotCount 변경 시나리오(증가/감소) 각각에서 마이그레이션 적용 후 `/player/state` 슬롯 응답 개수 == 설정값. 잉여 row가 DB에 남지 않음 확인
- **T3-S4. 두 레포 동기화 메커니즘** — **결정: 자동 스크립트 + CI 검사 (R2)**
  - 원본은 `stone_project`. 동기화 스크립트 작성(`stone_project`의 XML → `stone_server/Data/game-config.xml` 복사). `skins.csv`도 같은 스크립트로 묶을지 검토
  - **CI 일치 검사**: `stone_server` CI에 양쪽 XML 해시(또는 내용) 일치 검증 잡 추가. 불일치 시 CI fail로 드리프트 차단
  - **version 불일치 정책: 경고 후 진행 (R3)** — 클라/서버 부팅 시 XML `version`이 코드 기대값과 다르면 **경고 로그만 남기고 진행**(부팅 거부/폴백 아님). 단 CI 검사가 1차 방어선이므로 런타임 경고는 보조 안전망
  - verify: 동기화 스크립트 동작 + CI 검사 잡이 의도적 불일치 시 fail. version mismatch 시 경고 로그 출력 + 부팅 정상 진행 확인
- **T3-S5. 정합성 회귀 테스트**
  - 같은 XML로 클라 외삽 결과와 서버 derive 결과(슬롯 완성 페이스 등)가 일치하는지 교차 검증 케이스
  - verify: 동일 XML 입력 → 클라/서버 동일 슬롯 완성 시각 산출(허용 오차 내)

### 작업 4 — N Power(Enlightenment rate) 동기화 검증

- **T4-1. rate 동기화 경로 검증**
  - ⚠️ **(Codex 정정)** rate는 `/player/state`(`stateResponse.enlightenment_rate`)에서만 내려간다. `/player/sync`는 pts 누적 + last_sync_at만 반환하므로, "rate 동기화 충분" 결론은 **state 응답 기준**으로 재서술해야 한다. 만약 클라가 부팅 이후 state를 다시 안 받으면 rate 변경이 전파 안 되는 갭이 있는지 확인
  - `/player/state`의 `enlightenment_rate`가 클라 `anchorRate`로 주입되는지 코드 경로 확인 (클라가 GameConfig가 아닌 서버 권위값을 쓰는지)
  - verify: 서버 rate를 1.0이 아닌 값으로 바꿨을 때 클라가 state 재수신 후 외삽 기울기가 따라가는 테스트. rate 전파 트리거(부팅 외 재조회 시점) 명시
- **T4-2. 시계 왜곡/경계 검증**
  - 서버 `MAX(0, elapsed*rate)` 음수 방어, 클라 외삽 앵커 갱신(`anchorPts=balance_after`) 타이밍 점검
  - verify: last_sync_at 역전(시계 되돌림) 시 음수 누적 0 확인. pull 직후 앵커 재설정 확인
- **T4-3. 결론 문서화**
  - "기존 `/player/sync`로 rate까지 동기화 충분" 가정이 맞는지 결론. 추가 패킷 불필요 근거 또는 필요 시 갭 명시
  - verify: 검증 결과 요약 + 잔여 갭 목록(있으면)

---

## 3. 작업 순서 (의존성)

```
1) T1-1~T1-3  가챠 흐름 검증        → verify: 매핑표/에러규약/멱등성
2) T4-1~T4-3  rate 동기화 검증      → verify: rate 주입/경계/결론
   (1,2는 병렬 가능 — 읽기/검증 위주)
3) T3-S1      XML 스키마 확정        → 클라/서버 합의 (선행 게이트)
4) T3-S2 ∥ (T3-S3 → T3-S3b)  클라 로더 / 서버 로더+DB bootstrap → 각자 단위테스트 통과
5) T3-S4      동기화 메커니즘        → 절차/버전 정책
6) T3-S5      교차 정합성 회귀       → 최종 게이트
```

---

## 4. 리스크 · 미해결 질문

- **R1. 서버 cairn 상수의 런타임화 (T3-S3)** — `const` 기반 정수 나눗셈 로직을 config 주입으로 바꾸면 회귀 위험. 기존 테스트 그린 유지가 필수 게이트. **(Codex)** 난이도 '상', 주입 연쇄 4개 파일 + config validation 포함.
- **R2. 두 레포 동기화 (T3-S4)** — ✅ **결정: 자동 스크립트 + CI 일치 검사 도입.** 드리프트는 CI fail로 차단.
- **R3. version 불일치 정책** — ✅ **결정: 경고 후 진행.** 부팅 거부/폴백 아님. CI 검사가 1차 방어선, 런타임 경고는 보조.
- **R4. rate를 XML로 옮길지 (작업 3 vs 4 경계)** — Enlightenment rate는 현재 서버 DB 컬럼이 권위. 이를 XML 공통값으로 옮기면 DB 기본값과 충돌 가능 → 본 계획은 **rate를 XML 1차 대상에서 제외**(검토 항목)하고 작업 4는 검증으로 한정.
- **R5. 기존 슬롯 데이터 정리 (T3-S3b)** — ✅ **결정: 신규 마이그레이션으로 `slot_index >= SlotCount` row DELETE.** 런타임 필터링 아님.
- **R6. `/player/sync`에 rate 부재 (작업 4, Codex 신규)** — rate 전파가 `/player/state` 재수신에만 의존. 운영 중 rate 변경 시 클라 반영 트리거가 불명확.
- **R7. `player` 패키지 테스트 부재 (Codex)** — `go test ./internal/player`에 테스트 파일 없음. 작업 4 verify를 자동화하려면 `player` 테스트 신규 작성 필요.
- **Q1. ~~`maxTimeStones=3` 서버 대응값~~** — **(Codex 해결)** `migrations/000002` + `sqlitedb.go`에 `time_stone_count <= 3` 제약으로 이미 존재. 공통화 불필요.
- **Q2. XML 배치 위치** — 클라 `Resources/`(TextAsset) ↔ 서버 `Data/`. 파일명/경로 합의 필요.

### 검증 가능성 분리 (Codex)

verify 항목 중 일부는 `stone_server` 단독으로 측정 불가하다. 리뷰/실행 시 구분할 것:
- **서버 단독 검증 가능**: T1-2(에러 응답 규약), T1-3(멱등성), T3-S3/S3b(Go 테스트), T4-2(시계 경계, 단 `player` 테스트 추가 필요)
- **클라 레포(`stone_project`) 필요**: T1-1(DTO 역직렬화), T3-S2(클라 로더), T3-S5(교차 정합성), T4-1(클라 anchorRate 주입)

---

## 5. 산출물 (Definition of Done)

- [ ] 작업 1·2: 가챠 왕복/에러/멱등성 검증 보고서 + 보완 PR(필요 시)
- [ ] 작업 3: 공통 XML 스키마 + 클라 로더 + 서버 `pkg/gameconfig` + 동기화 절차 + 교차 회귀 테스트
- [ ] 작업 4: rate 동기화 검증 보고서 + 결론(추가 패킷 불필요 근거 또는 갭)
- [ ] 기존 단위테스트 전부 그린 유지
