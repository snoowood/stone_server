# Stone 클라이언트 ↔ 서버 패킷(API) 명세서

- 작성일: 2026-06-03
- 기준: `cmd/server/main.go` 라우트 + `internal/*` 핸들러 struct
- 프로토콜: **REST / JSON over HTTP**, 모든 경로 `/api/v1` prefix
- 직렬화: 서버 snake_case JSON ↔ 클라 PascalCase DTO (Newtonsoft `SnakeCaseNamingStrategy` 자동 변환)
- 시간: 전부 **RFC3339 UTC** (`2026-06-03T12:00:00Z`)
- 에러 공통 형식: `{"error": "<메시지>", "code": "<CODE>"}` (단, dev 전용 `/internal/dev-token` 은 `code` 없이 `error` 만 반환)

---

## 1. 전체 엔드포인트 개요

| # | 메서드 | 경로 | 인증 | 용도 | 요청 | 성공 |
|---|---|---|---|---|---|---|
| 1 | GET | `/health` | Public | 서버/의존성 상태 | — | 200/503 |
| 2 | POST | `/auth/steam` | Public | Steam 로그인 | `ticket` | 200 |
| 3 | POST | `/auth/refresh` | Public | JWT 재발급 | `refresh_token` | 200 |
| 4 | DELETE | `/auth/logout` | JWT 서명 | 로그아웃 | — | 204 |
| 5 | POST | `/time/sync` | Public | 서버 시간/드리프트 | `client_timestamp` | 200 |
| 6 | GET | `/player/state` | JWT | 플레이어 상태 전체 | — | 200 |
| 7 | POST | `/player/sync` | JWT | 권위 포인트 passive 정산 | — | 200 |
| 8 | POST | `/gacha/pull` | JWT | 가챠 뽑기(슬롯 완성 필요) | `slot_index` | 200 |
| 9 | GET | `/gacha/status` | JWT | 가챠 가능 여부 | — | 200 |
| 10 | GET | `/gacha/logs` | JWT | 가챠 로그(페이지) | `?page&limit` | 200 |
| 11 | POST | `/vow/pray` | JWT | 맹세 제작(재료 소비→스킨) | `materials`,`n_power` | 200 |
| 12 | POST | `/achievement/unlock` | JWT | 업적 해제 | `achievement_id` | 200 |
| 13 | GET | `/achievement/list` | JWT | 업적 목록 | — | 200 |
| 14 | POST | `/auth/dev` | Public(dev) | UUID 무비번 로그인 | `uuid` | 200 |
| 15 | GET | `/internal/dev-token` | dev | 로드테스트 토큰 | `?steam_id` | 200 |

> 인증: **Public**=토큰 불필요(+rate limit) / **JWT**=`Authorization: Bearer <jwt>` 필수 / **JWT 서명**=서명만 검증(로그아웃용) / **dev**=`APP_ENV != production` 에서만 등록.

---

## 2. AUTH (인증)

### 2.1 POST /auth/steam — Steam 로그인
**요청**
| 필드 | 타입 | 필수 | 설명 |
|---|---|---|---|
| `ticket` | string | ✅ | Steam 인증 티켓 |

**응답 200**
| 필드 | 타입 | 설명 |
|---|---|---|
| `jwt` | string | 액세스 토큰 |
| `refresh_token` | string | 리프레시 토큰 |
| `expires_at` | datetime | JWT 만료 |

**에러**: 400 `INVALID_REQUEST` / 401 `INVALID_TICKET` / 409 `TICKET_USED` / 409 `LOGIN_IN_PROGRESS` / 500 `INTERNAL_ERROR`

### 2.2 POST /auth/refresh — JWT 재발급
**요청**: `refresh_token` (string, ✅)
**응답 200**: `jwt`, `refresh_token`(신규, 기존 폐기), `expires_at`
**에러**: 400 `INVALID_REQUEST` / 409 `REFRESH_IN_PROGRESS` / 401 `INVALID_REFRESH_TOKEN` / 401 `REFRESH_TOKEN_REVOKED` / 500 `INTERNAL_ERROR`

### 2.3 DELETE /auth/logout — 로그아웃
**인증**: JWT 서명만. **요청**: 없음. **응답**: 204 No Content. **에러**: 500 `INTERNAL_ERROR`

### 2.4 POST /auth/dev — 개발 로그인 (dev 전용)
**요청**: `uuid` (string, ✅, 유효 UUID). **응답 200**: authResponse 동일. **에러**: 400 `INVALID_REQUEST` / 409 `LOGIN_IN_PROGRESS` / 500 `INTERNAL_ERROR`

### 2.5 GET /internal/dev-token — 로드테스트 토큰 (dev 전용)
**요청**: `?steam_id=<id>`. **응답 200**: authResponse 동일. **에러**: 400(steam_id 누락) / 500

---

## 3. PLAYER (플레이어 상태)

### 3.1 GET /player/state — 상태 전체 조회
**요청**: 없음. **응답 200 (`stateResponse`)**
| 필드 | 타입 | 설명 |
|---|---|---|
| `player_id` | string | 플레이어 UUID |
| `enlightenment_pts` | float | 현재 권위 포인트 잔고 |
| `enlightenment_rate` | float | 초당 권위 획득율(권위값, 클라 anchorRate 주입원) |
| `time_stone_count` | int | 타임스톤 개수 |
| `streak_days` | int | 연속 로그인 일수 |
| `next_gacha_at` | datetime? | 다음 가챠 시각(null 가능) |
| `last_sync_at` | datetime? | 마지막 sync 시각(null 가능) |
| `updated_at` | datetime | 갱신 시각 |
| `inventory` | inventoryItem[] | 인벤토리 |
| `cairn` | cairnState | WishCairn 슬롯 상태 |

**`inventory[]`**: `item_id`(string), `item_type`(string, 예 `stone_skin`/`wish_cairn_skin`), `rarity`(string), `count`(int), `source_type`(string, 획득 출처 예 `wish_cairn`/`vow_reward`/`unknown`), `acquired_at`(datetime)
**`cairn`**: `slot_count`(int), `max_layers`(int), `spawn_interval_sec`(int), `slots`(slot[])
**`cairn.slots[]`**: `slot_index`(int), `started_at`(datetime), `layer_count`(int), `status`(`building`|`complete`)

**에러**: 404 `NOT_FOUND` / 500 `INTERNAL_ERROR`

### 3.2 POST /player/sync — passive 정산
**요청**: 없음. **응답 200 (`syncResponse`)**
| 필드 | 타입 | 설명 |
|---|---|---|
| `enlightenment_pts` | float | 정산 후 잔고(= 기존 + MAX(0, elapsed×rate)) |
| `last_sync_at` | datetime? | 정산 시각 |

> ⚠️ 응답에 `enlightenment_rate` **없음** — rate 는 `/player/state` 로만 내려간다.
**에러**: 404 `NOT_FOUND` / 500 `INTERNAL_ERROR`

---

## 4. GACHA (가챠)

### 4.1 POST /gacha/pull — 가챠 뽑기
**요청**
| 필드 | 타입 | 필수 | 설명 |
|---|---|---|---|
| `slot_index` | int | ✅ | 완성된 WishCairn 슬롯 인덱스(0 ~ slot_count-1) |

**응답 200 (`pullResponse` ↔ 클라 `GachaPullResponseDto`)**
| 필드 | 타입 | 설명 |
|---|---|---|
| `item_id` | string | 획득 스킨 ID |
| `rarity` | string | 레어도 |
| `is_duplicate` | bool | 중복 여부 |
| `refund_points` | float | 항상 0(레거시 호환) |
| `next_gacha_at` | datetime | 다음 가챠 시각(쿨다운) |
| `balance_after` | float | 차감 후 권위 잔고 |
| `last_sync_at` | datetime | 가챠 시점 sync 시각 |
| `new_count` | int | 해당 아이템 누적 보유 수 |
| `source_type` | string | 획득 출처(항상 `wish_cairn`) |
| `reward` | rewardGrant | 공통 grant 메타(아래) |

**`rewardGrant`**: `item_id`(string), `item_type`(string, `stone_skin`), `rarity`(string), `is_duplicate`(bool), `new_count`(int), `source_type`(string), `acquired_at`(datetime)

**에러**
| HTTP | code | 클라 처리 |
|---|---|---|
| 400 | `INVALID_REQUEST` / `INVALID_SLOT` | 기본 실패 |
| 429 | `COOLDOWN_ACTIVE` (+`next_gacha_at`) | `/gacha/status` 재조회 |
| 403 | `CAIRN_INCOMPLETE` | 레이어 롤백 |
| 404 | `CAIRN_SLOT_NOT_FOUND` | (전용 처리 없음 — 갭 G1) |
| 409 | `INSUFFICIENT_POINTS` | 포인트 부족 안내 |
| 500 | `INTERNAL_ERROR` | 기본 실패 |

### 4.2 GET /gacha/status — 가챠 가능 여부
**응답 200**: `can_pull`(bool), `next_gacha_at`(datetime?, can_pull=false일 때만). **에러**: 500

### 4.3 GET /gacha/logs — 가챠 로그
**요청**: `?page=1&limit=20` (limit 1~100, 범위 밖이면 20)
**응답 200**: `logs`(logEntry[]), `total`(int64)
**`logEntry`**: `item_id`, `rarity`, `is_duplicate`(bool), `refund_points`(float), `cost_points`(float), `pulled_at`(datetime)
**에러**: 500

---

## 5. VOW (맹세 제작)

### 5.1 POST /vow/pray — 맹세 제작
재료 스킨 N개를 소비해 `base_rarity` 를 산출하고, `n_power`(소비 권위)에 비례한 성공률로
`target_rarity`(=base+1) 스킨 1개를 제작한다. 실패 시 `base_rarity` 스킨을 받는다.
**모든 판정은 서버 권위**(클라 계산값 불신).

**요청**
| 필드 | 타입 | 필수 | 설명 |
|---|---|---|---|
| `materials` | material[] | ✅ | 소비 재료. 합계 개수 = `vowRequiredItemCount`(game-config) |
| `n_power` | float | ✅ | 소비할 권위 포인트. `[tier.minNPower, tier.maxNPower]` 범위 |
| `debug_options` | object? | — | dev 전용, production 에선 무시 |

**`material`**: `item_id`(string), `count`(int)
**`debug_options`** (APP_ENV≠production 에서만 적용): `force_success`, `force_failure`, `infinite_n_power`, `ignore_required_items`, `local_pray` (모두 bool). `local_pray` 는 클라가 로컬 제작으로 처리할 때 쓰는 플래그(이 경우 서버 호출 자체를 안 함) — 서버는 필드를 받더라도 사용하지 않는다.

**응답 200**
| 필드 | 타입 | 설명 |
|---|---|---|
| `result` | string | `Success`/`Failed` (PascalCase — 클라 enum 비교) |
| `reward_item_id` | string | 지급 스킨 ID |
| `reward_rarity` | string | 성공=target, 실패=base |
| `reward` | rewardGrant | 공통 grant 메타(아래) |
| `is_duplicate` | bool | 중복 여부 |
| `new_count` | int | 해당 스킨 누적 보유 수 |
| `source_type` | string | 항상 `vow_reward` |
| `base_rarity` | string | 재료 평균 등급 |
| `target_rarity` | string | `base+1` |
| `success_rate` | float | 0~100 |
| `balance_after` | float | `n_power` 차감 후 잔고 |
| `last_sync_at` | datetime | 정산 시각 |
| `inventory` | inventoryItem[] | 갱신된 전체 인벤토리(`/player/state` 와 동일 형태) |
| `is_new_reward` | bool | `= !is_duplicate` |

**`rewardGrant`**: `item_id`(string), `item_type`(string, `stone_skin`), `rarity`(string), `is_duplicate`(bool), `new_count`(int), `source_type`(string, `vow_reward`), `acquired_at`(datetime)

**산식(클라 `VowSystem` 과 1:1)**
- 재료 등급 조건: `Common ≤ rarity < Legendary` (등급 서수 None=0, Common=1, …, Legendary=5)
- `base = floor(avg+0.5)`, `avg = Σ(order×count) / Σcount` (범위 `Common..<Legendary`), `target = base+1`
- `success_rate = clamp( lerp(minSuccessRate, maxSuccessRate, invlerp(minNPower, maxNPower, n_power)), 0, 100 )` — `maxNPower ≤ minNPower` 면 `maxSuccessRate`
- `n_power` 는 `enlightenment_pts` 에서 차감(cost). 재료는 트랜잭션에서 차감, 어느 하나 실패 시 전체 롤백.

**에러**
| HTTP | code | 발생 |
|---|---|---|
| 400 | `INVALID_REQUEST` | 본문 오류 / 재료 개수≠필요수 / 재료 등급 불가 / target tier 없음 / `n_power` 범위 밖 |
| 409 | `INSUFFICIENT_POINTS` | 권위 포인트 부족 |
| 409 | `INSUFFICIENT_MATERIALS` | 보유 재료 부족 |
| 500 | `INTERNAL_ERROR` | 기타 |

---

## 6. ACHIEVEMENT (업적)

### 6.1 POST /achievement/unlock — 업적 해제
**요청**: `achievement_id` (string, ✅)
**응답 200**: `ok`(bool), `already_unlocked`(bool, omitempty), `unlocked_at`(datetime?, omitempty), `steam_synced`(bool)
**에러**: 400 `INVALID_REQUEST` / 400 `CONDITION_NOT_MET` / 500

### 6.2 GET /achievement/list — 업적 목록
**응답 200**: `{ "achievements": achievementItem[] }`
**`achievementItem`**: `achievement_id`(string), `unlocked`(bool), `unlocked_at`(datetime?, unlocked일 때만), `steam_synced`(bool)
**에러**: 500

---

## 7. TIME / HEALTH

### 7.1 POST /time/sync — 서버 시간/드리프트
**요청**: `client_timestamp` (datetime, ✅)
**응답 200**: `server_time`(datetime, UTC), `drift_seconds`(int64, 절댓값), `warning`(bool, 드리프트>300초)
**에러**: 400 `INVALID_REQUEST`

### 7.2 GET /health — 헬스체크
**응답**: 200(정상) / 503(degraded). 필드: `status`(`ok`|`degraded`), `db`(`ok`|`error`), `cache`(`ok`|`error`), `version`

---

## 8. 공통 규약 요약

- **인증 헤더**: `Authorization: Bearer <jwt>` (JWT 엔드포인트). 만료 시 401 → 클라가 `/auth/refresh` 로 재발급(`ApiClient` 인터셉터 1회 자동 재시도).
- **에러 바디**: 대부분 `{"error","code"}` (클라는 `(HttpStatus, Code)` 조합으로 분기). 예외: dev 전용 `/internal/dev-token` 은 `error` 만.
- **시간 동기화**: 클라는 부팅 시 `/time/sync` 로 `ServerClock` 보정. 권위 시각(`last_sync_at`, `started_at`)은 서버 stamp 기준 외삽 → OS 시계 조작 무관.
- **오프라인 큐**: mutation 요청은 네트워크 오류 시 `OfflineQueue` 적재 후 재연결 시 재송출. 단 **`/gacha/pull` 은 의도적 제외**(실행 시점이 의미 있어 큐잉 위험).
- **rate(N Power) 전파**: `enlightenment_rate` 는 `/player/state` 단일 경로(부팅). 세션 중 변경 미반영(갭 G2). 양측 외삽은 `elapsed<0→0` 클램프.
- **WishCairn 동시성**: `/gacha/pull` 은 슬롯 CAS UPDATE 로 동시 청구 중 1건만 성공.
