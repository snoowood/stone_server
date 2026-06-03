# 검증 보고서 — T1(가챠 패킷) · T4(N Power/rate 동기화)

- 작성일: 2026-06-03
- 관련: 주간 계획서 T1·T4 (작업 1·2·4의 검증/보완 트랙)
- 범위: 기존 구현의 정합성 검증 + 커버리지 보완. 신규 엔드포인트 없음.

> **검증 경계 (Codex)**: 본 보고서는 두 레포를 대조한 결과다.
> - **이 레포(stone_server)에서 재현/자동검증됨**: 서버 Go 코드(필드 정의, 에러 응답, Sync 의 `MAX(0,...)`, `/player/state`만 rate 반환), `internal/player/player_test.go`.
> - **stone_project 감사 결과(이 레포에선 재현 불가)**: 클라 `GachaPullResponseDto` 매핑, 클라 에러 분기(409/429/403/404), `RecomputeFromAnchor` 음수 클램프, 클라가 `/player/sync` 미호출. → 클라 근거 `stone_project/...` 경로는 stone_project 레포 기준.

---

## T1 — 가챠 요청/응답 패킷

### T1-1. 왕복 필드 매핑 ✅ 일치

서버 `pullResponse`([internal/gacha/gacha.go:42](../../internal/gacha/gacha.go)) ↔ 클라 `GachaPullResponseDto`(stone_project `Network/Dto/GachaDto.cs:22`). 클라는 `SnakeCaseNamingStrategy`(Newtonsoft) 자동 변환 — 명시 `[JsonProperty]` 없음.

| 서버 JSON | 타입 | 클라 필드 |
|---|---|---|
| `item_id` | string | `ItemId` |
| `rarity` | string | `Rarity` |
| `is_duplicate` | bool | `IsDuplicate` |
| `refund_points` | float | `RefundPoints` (M3: 항상 0, 호환용) |
| `next_gacha_at` | datetime | `NextGachaAt` |
| `balance_after` | float | `BalanceAfter` |
| `last_sync_at` | datetime | `LastSyncAt` |
| `new_count` | int | `NewCount` |

→ 누락/타입 불일치 0건.

### T1-2. 실패 경로 응답 규약

서버 에러([gacha.go](../../internal/gacha/gacha.go)) ↔ 클라 분기(stone_project `WishCairnSystem.ApplyClaimResultAsync:555`):

| HTTP | code | 클라 처리 | 상태 |
|---|---|---|---|
| 409 | `INSUFFICIENT_POINTS` | `OnInsufficientPoints` 이벤트 | ✅ |
| 429 | `COOLDOWN_ACTIVE` (+next_gacha_at) | `/gacha/status` 재조회 | ✅ |
| 403 | `CAIRN_INCOMPLETE` | 레이어 롤백 + startedAt 조정 | ✅ |
| 404 | `CAIRN_SLOT_NOT_FOUND` | **전용 처리 없음** → 기본 fail 이벤트만 | ⚠️ 갭 (아래 G1) |
| 400 | `INVALID_SLOT`/`INVALID_REQUEST` | 기본 fail 이벤트 | ✅(클라가 slot 범위 보장) |

### T1-3. 재시도/멱등성 ✅

- `/gacha/pull` 은 **OfflineQueue 비대상**(의도적). 클라 `GachaApi.cs:17` 주석: 실행 시점이 의미 있어 큐잉 후 자동 재송출 위험.
- 중복 제출 방지: 서버 쿨다운(30분, KV + DB) → 2번째 요청 429. execPull 의 슬롯 CAS UPDATE 로 동시 두 요청 중 한쪽만 성공.

---

## T4 — N Power(Enlightenment rate) 동기화

### T4-1. rate 전파 경로 ✅ (단, 단일 경로)

- 서버 권위값 `enlightenment_rate`(DB 컬럼, 기본 1.0)는 **`GET /player/state` 응답으로만** 내려간다([player.go:46,106](../../internal/player/player.go)). `POST /player/sync` 응답(`syncResponse`)에는 **rate 없음**(pts + last_sync_at만).
- 클라는 `PlayerStateDto.EnlightenmentRate` → `EnlightenmentSystem.LoadFromServer`: `if (state.EnlightenmentRate > 0) anchorRate = state.EnlightenmentRate`(rate 누락 시 기존값 보존, silent drift 방지). 외삽: `currentEnlightenment = anchorPts + elapsed × anchorRate`.
- 클라는 `/player/sync` 를 **호출하지 않는다**(rate/잔고 권위는 `/player/state` 단일 소스).

### T4-2. 시계 왜곡/경계 ✅ 양쪽 클램프

- **서버**: Sync/execPull 모두 `MAX(0, COALESCE((now - last_sync_at)×rate, 0))` — 음수 elapsed(미래 last_sync_at) 시 잔고 감소 방지. last_sync_at 갱신을 같은 atomic UPDATE 로 처리(동시 호출 race 방지).
- **클라**: `RecomputeFromAnchor` 의 `if (elapsed < 0) elapsed = 0`. anchorAtUtc 는 server-stamped(`LastSyncAt`) 기준이라 OS 시계 조작 무관.
- **신규 테스트**(`internal/player/player_test.go`)로 검증: 누적(elapsed×rate), 미래 last_sync_at 클램프(잔고 불변), 상태 부재 시 404 + `code:"NOT_FOUND"`, GetState 의 `enlightenment_rate` 반환.

### T4-3. 결론

"기존 `/player/state` 경로로 rate 동기화 충분"은 **부팅 시점 기준 성립**한다. rate 는 state 응답으로 정확히 전파되고 양쪽이 음수 elapsed 를 클램프한다. 다만 아래 G2(세션 중 rate 변경 미반영)는 알려진 한계로 남긴다.

---

## 발견된 갭 (보완 후보)

### G1. 404 CAIRN_SLOT_NOT_FOUND 클라 미처리 (저위험)
- 현상: 서버가 슬롯 row 부재 시 404 반환하지만, 클라는 전용 분기 없이 기본 fail 이벤트만 발사(상태 복구 없음).
- 발생 가능성: 낮음 — 클라는 자신이 아는 슬롯(0..SlotCount-1)만 pull 하고, `/player/state` lazy-init 이 슬롯을 보장.
- 권장: 404 시 `/player/state` 재로드 트리거(슬롯 재초기화). **클라 변경 + Unity 검증 필요** → 별도 처리 권장.

### G2. rate 단일 경로 — 세션 중 변경 미반영 (알려진 한계, 계획서 R6)
- 현상: rate 는 부팅 시 `/player/state` 로만 주입. 운영 중 서버가 rate 를 바꿔도 클라는 다음 부팅 전까지 미반영.
- 현재 영향: rate 는 사실상 고정값(1.0)이라 실질 문제 없음.
- 권장: rate 운영 변경을 도입할 거면 `/player/sync` 응답에 rate 포함 또는 주기적 `/player/state` 재조회. 현 시점 보류.

---

## 산출물

- ✅ `internal/player/player_test.go` 신규 — Sync 누적/클램프/404+NOT_FOUND + GetState rate (서버 `player` 패키지 테스트 부재 갭 해소)
- ✅ 본 검증 보고서 (T1/T4 필드매핑·에러규약·멱등성·rate경로·시계왜곡 검증)
- ⬜ G1(404 클라 처리), G2(rate 재조회) — 옵션 보완, 클라 변경 수반
