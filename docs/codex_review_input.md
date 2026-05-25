# Stone Server 분석·제안 결과물 (리뷰 요청 대상)

> 다른 AI(Claude)가 클라이언트(`F:\stone_project`, Unity C#)와 서버(`F:\stone_server`, Go) 양쪽 코드베이스를 분석해 작성한 결과물. 사실관계 오류, 논리적 비약, 더 나은 접근, 빠진 고려사항을 식별해주세요.

---

## 1. 시스템 매핑 분석

| 시스템 | 서버 (Go) | 클라이언트 (Unity) | 권한 | 정합성 |
|---|---|---|---|---|
| N Power (Enlightenment Pts) | `internal/player/player.go:51-98` — `POST /api/v1/player/sync` 가 `last_sync_at` 기준 delta 계산해서 적립 | `Assets/Scripts/Systems/EnlightenmentSystem.cs` — 로컬에도 `passiveGainPerSecond=0.5/s` 자체 증가 | 서버 권위 | ⚠️ 이중 계산 위험 |
| 돌탑 (WishCairn) | `time_stone_count SMALLINT CHECK ≤ 3` 한 필드만 존재. 누적/만료 로직 없음 | `Assets/Scripts/Systems/WishCairnSystem.cs` — 5 슬롯 × 5층 = 최대 25개 상태를 클라가 관리, 30초마다 층 추가 | **클라 권위** | ❌ 불일치 |
| 가챠 뽑기 | `internal/gacha/rng.go` — `POST /api/v1/gacha/pull` 100pt 소비, 5등급 RNG, pity, 쿨다운 30분 | `GachaApi.cs` `PullAsync()` 호출 → `GachaPullResponseDto` 수신 | 서버 권위 | ✅ |
| 합성 (Crafting) | **없음** | **없음** | — | ❌ 양쪽 미구현 |
| 5등급 enum | `common/uncommon/rare/unique/legendary` (소문자) | `Rarity` enum + `None` 추가 | — | ✅ |
| 인벤토리 | `inventories` 테이블 `(player_id, item_id) UNIQUE`, 중복 시 자동 refund | `InventorySystem.LoadFromServer()` 전체 교체 | 서버 권위 | ✅ |

---

## 2. Protocol 정합성 문제점 (식별된 5건)

1. **돌탑이 사실상 클라 권위**: 서버 `time_stone_count` SMALLINT(≤3) 단일 카운터만 존재. 클라는 5×5=25상태 자체 관리. `TimeStoneCount` 가 dead field. 클라 메모리 변조 시 가챠 시도 위조 가능(쿨다운 30분으로 일부 방어).

2. **`enlightenment_rate` 클라 미수신**: `PlayerStateDto` 주석에 *"Phase 4 도입 예정"* 명시. 클라는 `passiveGainPerSecond=0.5` 하드코딩, 서버 기본 rate 는 1.0 → UI 표시값과 서버 잔고 어긋남.

3. **합성 시스템 프로토콜 자체 없음**: DTO·엔드포인트·테이블 모두 부재.

4. **돌탑 인터벌이 서버 설정에 없음**: `GameConfig.wishCairnStoneSpawnIntervalSeconds=30` 클라 하드코딩. 운영 중 밸런싱 변경 시 빌드 재배포 필요.

5. **`/gacha/pull` 응답에 변경된 `enlightenment_pts` 없음**: 클라가 100 차감 + refund 직접 계산. 누락 시 표시값 어긋남.

---

## 3. 게임 부팅 시나리오 (프로토콜 기반)

```
[부팅]
  ├─ GET    /api/v1/health                (선택)
  ├─ POST   /api/v1/time/sync             (drift 체크)
  ├─ POST   /api/v1/auth/steam            (JWT 획득)
  └─ GET    /api/v1/player/state          (잔고·인벤·쿨다운·streak 일괄)

[메인 루프]
  ├─ 60s ─▶ POST /api/v1/player/sync      (포인트 적립 정산)
  ├─ 30s ─▶ [로컬 only] 돌탑 층 +1
  └─ 돌탑 5층 완성
       ├─▶ GET  /api/v1/gacha/status      (선택)
       └─▶ POST /api/v1/gacha/pull
              ├─ 200: 인벤 추가 / 중복이면 refund
              ├─ 409 INSUFFICIENT_POINTS  → 안내
              └─ 429 COOLDOWN_ACTIVE      → 다음 시각 표시

[종료]
  ├─ POST   /api/v1/player/sync           (마지막 정산)
  └─ DELETE /api/v1/auth/logout
```

---

## 4. 사용자 피드백 5건 → 작업 계획

### F1. 중복 아이템 스택 증가
- **현재**: 서버 `INSERT ... ON CONFLICT DO NOTHING` + refund 지급. 클라는 `if (!dto.IsDuplicate)` 분기로 AddItem 호출 안 함.
- **클라 UI는 이미 `GetItemCountBySkinId`, `GetGroupedSlots` 로 그룹 카운팅 구조 보유**.
- **작업**: 
  - `inventories` 에 `count INT NOT NULL DEFAULT 1` 추가 마이그레이션
  - `INSERT ... ON CONFLICT DO UPDATE SET count = count + 1 RETURNING is_new`
  - `pullResponse` 에 `new_count` 필드 추가, `refund_points` 폐지 권장
  - `InventoryItemDto`, `GachaPullResponseDto` 에 `Count`/`NewCount` 필드 추가
  - `WishCairnSystem.cs:481` 의 `IsDuplicate` 분기 제거
- **추정**: 서버 1d + 클라 0.5d

### F2. 돌탑 싱크
- **3가지 방안 비교**:
  | 방안 | 부하 | 보안 | 작업량 |
  |---|---|---|---|
  | A. 결정론적 시뮬레이션 (cairn_started_at + seed) | 매우 낮음 | 중 | 중 |
  | B. 서버 권위 (cairn_state JSONB) | 중간 | 높음 | 큼 |
  | C. **하이브리드 (권장)** | 낮음 | 중상 | 중 |
- **C 안 설계**:
  ```
  마이그레이션: 
    player_states ADD COLUMN cairn_started_at TIMESTAMPTZ
    player_states ADD COLUMN cairn_state JSONB
    player_states DROP COLUMN time_stone_count
  새 엔드포인트:
    POST /api/v1/cairn/sync — 클라 상태 제출 → 서버 권위 상태 응답
    POST /gacha/pull 본문에 { slot_id, expected_layer_count } 추가 → 서버 검증, 미달 시 403 CAIRN_INCOMPLETE
  ```
- **추정**: 서버 2d + 클라 1.5d

### F3. config 동기화 (서버 ↔ 클라)
- **현재 분포 (변경 시 양쪽 같이 고쳐야 하는 값들)**: pullCost, 쿨다운, 확률표, refund표, 누적률, 돌탑 인터벌/슬롯/층수 등 분산
- **방안**: `/api/v1/config` 엔드포인트 신설. 클라 부팅 시 GET → GameConfig SO 메모리 덮어쓰기. version hash 로 캐싱.
- **추정**: 서버 1d + 클라 0.5d

### F4. 네이밍 통일
- **불일치**: enlightenment_pts ↔ EnlightenmentPts ↔ "해탈력" ↔ "N Power", WishCairn ↔ time_stone ↔ "돌탑", gacha ↔ Claim/Pull, item ↔ skin
- **권장 용어집**: `power`/`stone_tower`/`skin`/`skin_pull` 표준화. enum `Rarity.None` 은 DTO 에서 string? 처리
- **추정**: 컨벤션 합의 0.5d + 일괄 리네임 PR 1d

### F5. CSV 마스터 데이터 파이프라인
- **구조**: `data/skins.csv` 단일 소스 → 서버 `go:embed` 캐싱, 클라 Unity Editor 임포터로 SkinData SO 자동 생성
- **CSV vs DB 비교**: 변경 빈도 낮으면 CSV 임베드 (운영 immutable, 안전). LiveOps 필요 시점에 DB 적재로 단계적 전환
- **추정**: 서버 1d + Unity 임포터 1d + CSV 마이그레이션 0.5d

---

## 5. 사용자 시나리오 3건 → 코드 적합도

| 시나리오 | 흐름 | 판정 |
|---|---|---|
| S1. 새 아이템 정상 획득 | time/sync → auth → state → 적립 → gacha/pull (200, !duplicate) → InventorySystem.AddItem | ✅ 정상 (잔고 표시 어긋남만 F3 으로 보완) |
| S2. N Power 부족 → 돌탑 유지 | gacha/pull → 409 INSUFFICIENT_POINTS → 슬롯 보존 + OnInsufficientPoints | ✅ 정상 (현재 코드로 동작) |
| S3. 중복 → 스택 +1 | gacha/pull → 200 (duplicate) → 클라 AddItem 안 부름 / 서버 INSERT 안 됨 | ❌ **불가능. F1 작업 완료 후 동작** |

---

## 6. 우선순위 권장

| 순위 | 작업 | 이유 | 추정 |
|---|---|---|---|
| 1 | F1 중복 스택 | S3 직접 차단, DB 마이그레이션 비용 작음 | 1.5d |
| 2 | F5 CSV 파이프라인 | 이후 작업의 SSOT 확보, F4 와 결합 | 2.5d |
| 3 | F3 /config 엔드포인트 | F1·F2 진행 중 값 변경 부담 제거 | 1.5d |
| 4 | F4 네이밍 통일 | F3 config 키가 곧 표준 | 1.5d |
| 5 | F2 돌탑 싱크 | 클라우드 세이브 시점에 진행 | 3.5d |

---

## 리뷰 요청 사항

1. **사실관계 오류**: 위 분석 중 코드 실제 동작과 어긋난 부분이 있는가? (실제 파일 확인 권장: `internal/gacha/gacha.go`, `internal/player/player.go`, `migrations/000003_create_inventories.up.sql`, `Assets/Scripts/Systems/WishCairnSystem.cs`, `Assets/Scripts/Systems/InventorySystem.cs`, `Assets/Scripts/Network/Dto/*.cs`)
2. **누락된 위험**: 우선순위 1~5 외에 즉시 다뤄야 할 보안/성능/데이터 무결성 이슈가 있는가?
3. **더 나은 접근**: 각 피드백(F1~F5)에 대해 제안된 방안보다 나은 설계가 있는가?
4. **우선순위 재평가**: 제시된 순서가 합리적인가? 의존성 그래프가 잘못된 부분은?
5. **F2 돌탑 싱크 방안 C(하이브리드)** 의 결정론적 시뮬레이션 부분 — 클라/서버 RNG 동기화 구현 시 흔히 빠지는 함정은?
6. **F1 의 refund 폐지** 가 적절한가? 스택 + refund 동시 운영 패턴의 다른 사례는?
