# Stone Tamagotchi 컨텐츠 용어집

- 작성일: 2026-06-03
- 기준: `stone_project`(Unity 클라) + `stone_server`(Go 서버) 실제 코드 검증
- 목적: 게임 컨텐츠 개념 ↔ 코드 식별자 ↔ 패킷 필드 매핑 (기획/개발 공통 어휘)

> 게임 컨셉: **돌(Stone)을 키우는 데스크탑 다마고치**. 돌이 "해탈"(깨달음)을 쌓고, "소원돌탑"으로 스킨을 수집해 돌을 꾸민다.

---

## 1. 핵심 자원

### 해탈력 (Enlightenment) — 사용자 통칭 "N Power"
| 항목 | 내용 |
|---|---|
| 코드명 | `enlightenment_pts`(잔고), `enlightenment_rate`(초당 획득율) |
| 의미 | 게임의 주 자원. 가챠(소원돌탑 수확) 비용으로 소비 |
| 획득 | ① 키보드/마우스 입력 1회당 `pointsPerInput`(=1) ② 시간 경과 passive 누적(`rate`, 기본 1.0/초) ③ 랜덤 이벤트 보상 ④ 일일 스트릭 배수 |
| 권위 | **서버 권위**. 클라는 `anchorPts + (now - anchorAt) × anchorRate` 로 외삽 표시 |
| 패킷 | `/player/state`(rate 포함), `/player/sync`(pts 정산, rate 없음) |

### 타임스톤 (TimeStone)
| 항목 | 내용 |
|---|---|
| 코드명 | `time_stone_count`, `maxTimeStones`(=3) |
| 의미 | 일일 로그인 스트릭이 끊길 위험 시 소비해 스트릭을 보전하는 아이템 |
| 보유 한도 | 3개 (DB `CHECK (time_stone_count <= 3)`) |

---

## 2. WishCairn (소원돌탑) — 가챠 시스템

> Cairn = 돌을 쌓은 탑. 슬롯마다 돌 층이 시간에 따라 쌓이고, 다 차면 수확해 스킨을 얻는다.

| 용어 | 코드명 | 의미 |
|---|---|---|
| 소원돌탑 | `WishCairn` | 슬롯 여러 개로 된 가챠 시스템 |
| 슬롯 | `slot`, `slot_index` | 개별 가챠 칸. 기본 `slotCount`=5개 |
| 층 | `layer`, `layer_count` | 슬롯에 쌓이는 돌 단계. 완성까지 `maxLayers`=5층 |
| 생성 간격 | `spawnIntervalSeconds`=30 | 30초마다 1층 자동 증가 |
| 시차 | `PhaseOffset` = `spawnInterval×maxLayers/slotCount` | 슬롯이 동시에 완성되지 않게 시작 시각을 어긋나게 둠 |
| 슬롯 상태 | `building` / `complete` (서버) · Empty/Building/Complete/Claimed (클라) | 층수 < max → building, ≥ max → complete |
| 수확(청구) | `claim` = `POST /gacha/pull` | complete 슬롯에서 스킨 획득 + 슬롯 리셋 |
| 가챠 비용 | `wishCairnCost`=100 | 수확 1회당 해탈력 소비 |
| 쿨다운 | `wishCairnCooldownMinutes`=30(=1800초) | 수확 간 대기 |

**층 계산 공식 (클라/서버 동일)**: `layers = floor((now - started_at) / spawnIntervalSeconds)`, `[0, maxLayers]` 클램프, `elapsed<0 → 0`.

---

## 3. 스킨 (Skin)

가챠로 획득하는 돌 꾸미기 아이템. 마스터 데이터: `stone_server/Data/skins.csv` (`skinId, partType, rarity`).

### 부위 (partType) — 6종
| 부위 | 의미 |
|---|---|
| `top` | 상단(머리/모자 등) |
| `bottom` | 하단(꼬리/하의 등) |
| `outfit` | 전신 의상 |
| `accessory` | 악세서리 |
| `face` | 얼굴 표정 |
| `effect` | 주변 이펙트 |

### 희귀도 (rarity) — 5종 + 확률 (서버 `rng.go` 검증값)
| 등급 | 확률 | 비고 |
|---|---|---|
| `common` (흔함) | **80.00%** | 0~8000/10000 |
| `uncommon` (보통) | **10.00%** | 8000~9000 |
| `rare` (희귀) | **5.00%** | 9000~9500 |
| `unique` (고유) | **4.90%** | 9500~9990 |
| `legendary` (전설) | **0.10%** | 9990~10000 |

> 추첨 순서: rarity(가중) → partType(균등) → skinId(균등).

### skinId prefix — 출처 분류
| prefix | 의미 | 예시 |
|---|---|---|
| `standard_` | 기본 스킨 | `standard_a-noble-womans-hair` |
| `season_` | 시즌/이벤트 | `season_halloween_dracula-cloak` |
| `archivement_` | 업적 보상 (오타 그대로 사용) | `archivement_a_barley_tree` |

---

## 4. 일일 스트릭 (Daily Streak)

| 항목 | 내용 |
|---|---|
| 코드명 | `streak_days`, `StreakDays` |
| 의미 | 연속 로그인 일수 |
| 증가 | 어제 접속 후 오늘 첫 접속 시 +1. 미접속 일수는 타임스톤으로 보전 가능 |
| 초기화 | 미접속 + 타임스톤 부족 시 1로 리셋 |
| 해탈력 배수 | `GetMultiplier() = 1.0 + 4.0×(streakDays/365)`, **x1.0(1일) ~ x5.0(365일+)** |
| anti-cheat | `ServerClock` 기준 일자 비교(OS 시계 조작 방지) |

---

## 5. 랜덤 이벤트 (RandomEvent) — 4종

| 이벤트 | 코드명 |
|---|---|
| 나비 | `Butterfly` |
| 떨어지는 꽃잎 | `FallingPetal` |
| 반딧불이 | `Firefly` |
| 이슬방울 | `Dewdrop` |

| 항목 | 값 |
|---|---|
| 발생 간격 | `eventMinInterval`=120 ~ `eventMaxInterval`=300초 랜덤 |
| 지속 시간 | 10초 |
| 방치(타임아웃) 보상 | `doNothingReward`=50 해탈력 |
| 클릭 보상 | `clickReward`=5 해탈력 |

---

## 6. 도감 · 인벤토리

| 용어 | 코드명 | 의미 |
|---|---|---|
| 도감 | `Collection` | 획득한 스킨 발견 기록(첫 획득 시각). 서버 inventory 기반 재구성 |
| 인벤토리 | `Inventory`, `inventories` | 보유 스킨. **집계형 stack 모델**: `(item_id, count)` 1행 1스킨, 재획득 시 `count` 증가 |

> 중복 가챠 보상은 환불(`refund_points`, 항상 0/레거시) 대신 `count` 증가로 처리(M3).

---

## 7. 업적 (Achievement) — 6종

서버 권위 판정(`internal/achievement/conditions.go`). 클라는 트리거만, 서버가 조건 검증 후 Steam 동기화.

| 업적 ID | 조건 |
|---|---|
| `ACH_FIRST_PULL` | 첫 가챠 수확 |
| `ACH_RARE_UNLOCK` | rare 이상 스킨 획득 |
| `ACH_LEGENDARY` | legendary 스킨 획득 |
| `ACH_STREAK_7` | 스트릭 7일 |
| `ACH_STREAK_30` | 스트릭 30일 |
| `ACH_COLLECTOR` | 서로 다른 스킨 10종 수집 |

---

## 8. 기술 · 동기화 용어

| 용어 | 의미 |
|---|---|
| 서버 권위 (server-authoritative) | 잔고·rate·슬롯·업적은 서버가 최종 결정. 클라는 표시/외삽만 |
| anchor (앵커) | 클라 외삽 기준점. `anchorPts`(잔고 기준값) + `anchorAtUtc`(기준 시각) + `anchorRate`(기울기) |
| 외삽 (extrapolation) | 다음 서버 동기화 전까지 클라가 `anchor + elapsed×rate` 로 표시값 추정 |
| ServerClock | 서버 stamp 시각 기준 클럭(`/time/sync` 보정). OS 시계 조작 무관 |
| 권위 시각 | `last_sync_at`(해탈력), `started_at`(슬롯) — 서버 stamp |

---

## 9. 시스템 ↔ 컨텐츠 매핑 (클라 `Assets/Scripts/Systems/`)

| 시스템 | 담당 컨텐츠 |
|---|---|
| `EnlightenmentSystem` | 해탈력 잔고/passive 누적/외삽/서버 동기화 |
| `WishCairnSystem` | 소원돌탑 슬롯/층 성장/수확/가챠 |
| `DailyStreakSystem` | 연속 로그인/해탈력 배수 |
| `TimeStoneSystem` | 타임스톤 보유/소비 |
| `RandomEventSystem` | 랜덤 이벤트 발생/보상 |
| `CollectionSystem` | 스킨 도감(발견 기록) |
| `InventorySystem` | 스킨 보유(count stack) |
| `SkinManager` / `SkinCatalog` | 스킨 장착/메타데이터 조회 |
| `AchievementSystem` | 업적 트리거(서버 판정 요청) |

---

> 패킷(API) 명세: [api-packet-spec.md](api-packet-spec.md) 참조.
