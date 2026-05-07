# Stone Server — 시스템 상세 설계
> v1.5 — 5차 피드백 반영 (2026-05-03) — 최종 확정

## 1. Steam 서버 인증

### 목적
클라이언트가 제출한 Steam 티켓을 Steamworks Web API로 검증하고, 이후 모든 API 요청에 사용할 JWT를 발급한다.

### 흐름

```
Client                    Go Server              Steamworks Web API
  │                           │                         │
  │ GetAuthTicketForWebApi()  │                         │
  │ (identity: stone-server)  │                         │
  │ ──────────────────────►   │                         │
  │                           │                         │
  │ POST /auth/steam           │                         │
  │ { ticket }                │                         │
  │ ──────────────────────►   │                         │
  │                           │ GET /ISteamUserAuth/    │
  │                           │ AuthenticateUserTicket  │
  │                           │ ─────────────────────►  │
  │                           │ ◄─────────────────────  │
  │                           │ { steamid, result }     │
  │                           │                         │
  │                           │ steam_id을 신뢰 원본으로  │
  │                           │ 사용 (클라이언트 값 무시) │
  │                           │                         │
  │ ◄──────────────────────   │                         │
  │ { jwt, refresh_token,     │                         │
  │   expires_at }            │                         │
```

### 핵심 규칙
- 클라이언트는 `GetAuthTicketForWebApi(identity: "stone-server")`로 티켓 발급 (`GetAuthSessionTicket()` 아님)
- steam_id는 **Steamworks 검증 결과에서만** 신뢰, 클라이언트 전송값 무시
- 티켓은 **1회 사용 후 폐기** (Redis에 티켓 해시 저장, 재사용 차단)
- JWT jti를 Redis에 저장 (`session:{jti}` TTL 24h) — 로그아웃 시 즉시 무효화 가능
- JWT 유효기간: **24시간**, Refresh Token: **30일**

### Refresh Token 갱신 시 jti 전환 규칙

`POST /auth/refresh` 호출 시 새 JWT를 발급하면서 이전 JWT의 jti를 원자적으로 교체한다.

```
POST /auth/refresh 동작 (원자적 jti 전환):
1. refresh:{player_id} 에서 저장된 토큰과 요청 토큰 비교 → 불일치 시 401
2. GET session:current:{player_id} → old_jti 조회
3. 새 JWT 생성 (new_jti)
4. Lua 스크립트 원자적 처리:
   - DEL session:{old_jti}          (이전 JWT 즉시 무효화)
   - SET session:{new_jti} TTL 24h  (새 JWT 등록)
   - SET session:current:{player_id} = new_jti TTL 24h
5. 새 JWT 반환 (refresh_token은 재발급하지 않음, 30일 고정)
```

> 이 원자적 처리로 refresh 이후 이전 JWT로 요청 시 즉시 401이 반환된다.

### 세션 경합 처리 규칙 (로그아웃 compare-and-delete)

새 로그인과 기존 세션 로그아웃 요청이 거의 동시에 들어오는 경합 상황을 처리하기 위해 **compare-and-delete** 방식을 적용한다.

```
로그아웃 처리 순서:
1. 요청 JWT에서 jti 추출
2. Redis GET session:current:{player_id} → stored_jti 조회
3. stored_jti == 요청 jti 일 때만:
   - session:{jti} 삭제
   - session:current:{player_id} 삭제
   - refresh:{player_id} 삭제
4. stored_jti != 요청 jti 이면:
   - session:{jti} 만 삭제 (이미 새 세션으로 교체된 상태이므로 current/refresh 건드리지 않음)
   - 204 반환 (정상 처리)
```

> Lua 스크립트 또는 Redis의 원자적 GET→비교→DEL 조합으로 구현한다.

---

## 2. 가챠 시스템 (사리라 뽑기)

### 목적
클라이언트 사이드 RNG를 제거하고 서버에서 결과를 결정한다.  
쿨다운 및 비용(깨달음 포인트)을 서버에서 강제 적용한다.

### 흐름

```
Client                    Go Server              PostgreSQL / Redis
  │                           │                         │
  │ POST /gacha/pull           │                         │
  │ ──────────────────────►   │                         │
  │                           │ Redis: next_gacha_at    │
  │                           │ 확인 (쿨다운 중이면     │
  │                           │ 429 반환)               │
  │                           │ ─────────────────────►  │
  │                           │ ◄─────────────────────  │
  │                           │                         │
  │                           │ BEGIN TRANSACTION       │
  │                           │ ├ 포인트 잔액 확인/차감  │
  │                           │ ├ 서버 RNG 실행          │
  │                           │ ├ 결과 아이템 결정        │
  │                           │ ├ 인벤토리 추가           │
  │                           │ └ gacha_logs 기록        │
  │                           │ COMMIT                  │
  │                           │                         │
  │                           │ Redis: next_gacha_at    │
  │                           │ 갱신 (TTL 30분)          │
  │                           │                         │
  │ ◄──────────────────────   │                         │
  │ { itemId, rarity,         │                         │
  │   next_gacha_at }         │                         │
```

### 핵심 규칙
- 확률 테이블은 **서버에만 존재** (클라이언트에 미노출)
  - Common 60% / Uncommon 30% / Rare 9% / Unique 0.9% / Legendary 0.1%
- 쿨다운 만료 시각은 `next_gacha_at`으로 통일 (Redis TTL + DB 컬럼 동일 명칭)
- 쿨다운 중 요청 시 **`429` 반환** (`next_gacha_at` 포함)
- **Redis 쿨다운 유실 복구**: Redis에서 `next_gacha_at` miss 발생 시 DB `player_states.next_gacha_at`을 fallback으로 조회하여 복구. DB도 null이면 쿨다운 없음으로 간주.
- 포인트 차감 + 인벤토리 추가 + 로그 기록은 **단일 트랜잭션**
- 감사용 시드: 원문 저장 대신 **SHA-256 해시** 저장 (`gacha_seed_hash`)
- 중복 아이템 획득 시: 인벤토리 저장 대신 **포인트 환급** 처리
  - 환급량은 rariry별 고정값 (별도 GameConfig에 정의)
- **Pity 시스템**: `pity_count` 컬럼 설계에 포함, 실제 발동 로직은 Phase 이후 확장

---

## 3. 시간 어뷰징 검증

### 목적
클라이언트의 시스템 시계를 조작하여 쿨다운(가챠, 일일 스트릭, 랜덤 이벤트)을 우회하는 것을 차단한다.

### 흐름

```
Client                    Go Server
  │                           │
  │ POST /time/sync            │
  │ { client_timestamp }      │
  │ ──────────────────────►   │
  │                           │ server_time = time.Now().UTC()
  │                           │ drift = |server_time - client_timestamp|
  │                           │
  │ ◄──────────────────────   │
  │ { server_time, drift_     │
  │   seconds, warning }      │
  │ (drift > 5분이면           │
  │  warning: true)           │
```

### 핵심 규칙
- **모든 시간 기준은 서버 UTC** — 클라이언트 시간은 UI 표시용, 신뢰하지 않음
- 쿨다운 만료 시각 `next_gacha_at`은 DB/Redis에 서버 시간으로 저장
- 클라이언트 시간 drift가 **5분 초과** 시 `warning: true` 반환 (강제 차단 없음, UI 경고용)
- 일일 스트릭 판정: 서버의 UTC 날짜 기준 (`last_login_date` 비교)
- 시간 sync 엔드포인트는 **인증 불필요** (게임 시작 전 호출 가능)

---

## 4. 인벤토리 동기화

### 목적
클라이언트의 로컬 JSON 저장을 서버 PostgreSQL로 대체하여 데이터 위변조 및 클라이언트 삭제로 인한 데이터 소실을 방지한다.

### 흐름

```
Client                    Go Server              PostgreSQL
  │                           │                      │
  │ [로그인 시]                │                      │
  │ GET /player/state          │                      │
  │ ──────────────────────►   │                      │
  │                           │ SELECT 전체 상태      │
  │ ◄──────────────────────   │ ◄─────────────────   │
  │ { inventory, points, ... }│                      │
  │                           │                      │
  │ [클릭 포인트 배치 전송]    │                      │
  │ POST /player/clicks        │                      │
  │ { count: 50 }             │                      │
  │ ──────────────────────►   │                      │
  │                           │ points += 50 ×       │
  │                           │ pointsPerClick       │
  │ ◄──────────────────────   │ (서버 계산)           │
  │ { enlightenment_pts }     │                      │
```

### 핵심 규칙
- **서버가 권위(Source of Truth)** — 클라이언트 delta 값을 그대로 반영하지 않음
- 상태 변경은 항상 **도메인 이벤트 API**를 통해서만 발생
  - 포인트 획득: `POST /player/clicks` (클릭 횟수 → 서버 계산)
  - 아이템 획득: `POST /gacha/pull` 내부에서 처리
  - 업적 보상: `POST /achievement/unlock` 내부에서 처리
- 범용 `PATCH /player/state`는 존재하지 않음
- 클라이언트 로컬 저장(30초 자동저장)은 UI 반응성용 임시 캐시로만 사용
- 다중 기기 동시 사용 미지원 (단일 활성 세션 정책, `00_overview` 참조)

### 클릭 이벤트 검증 규칙 (anti-cheat)

`POST /player/clicks`는 포인트 수급의 주요 경로이므로 서버에서 다음 조건을 검증한다.

| 규칙 | 기준 | 위반 시 |
|------|------|---------|
| 단일 배치 최대 클릭 수 | 요청당 `count ≤ 300` | `400 INVALID_COUNT` |
| 시간당 처리 상한 | 플레이어당 시간당 최대 3,000 클릭 | `429 RATE_EXCEEDED` |
| 배치 중복 제출 방지 | 요청에 `batch_id`(UUID) 포함, 서버가 Redis에서 중복 확인 | `409 DUPLICATE_BATCH` |
| 최소 전송 간격 | 같은 플레이어의 연속 요청 간격 ≥ 1초 | `429 TOO_FREQUENT` |

```
Request: { "batch_id": "uuid-v4", "count": 50 }
Redis 키: click:batch:{player_id}:{batch_id}  TTL: 300s (5분, 지연 재전송 방어창)
```

> **anti-replay 창 근거**: TCP 재시도는 통상 60초 이내, 의도적 지연 재전송까지 커버하기 위해 TTL을 5분으로 설정한다. 5분 이후의 동일 batch_id 재전송은 사실상 새 배치로 간주한다. 이 창보다 긴 지연이 발생하는 상황은 네트워크 장애로 보고 클라이언트에서 새 batch_id로 재시도하도록 한다.
>
> **⚠️ 의도적 운영 타협**: 5분 초과 패킷 지연으로 인한 동일 batch_id 재도착은 중복으로 감지되지 않아 포인트가 이중 적립될 수 있다. 이 리스크는 현재 서비스 규모에서 수용 가능한 범위로 판단하여 방지 범위 밖으로 허용한다. 5분 이내 중복만 방지 보장한다.

---

## 5. Steam 업적 시스템

### 목적
클라이언트가 직접 Steam 업적을 unlock하면 핵 도구로 임의 unlock 가능하므로, **서버에서 조건 검증 후** Steamworks Web API를 통해 unlock한다.

### 흐름

```
Client                    Go Server              Steamworks Web API
  │                           │                         │
  │ POST /achievement/unlock   │                         │
  │ { achievement_id }        │                         │
  │ ──────────────────────►   │                         │
  │                           │ ① DB 조건 검증           │
  │                           │ (이미 unlock → 200 즉시) │
  │                           │ (조건 미충족 → 400)      │
  │                           │                         │
  │                           │ ② DB 업적 기록           │
  │                           │ steam_synced = false    │
  │                           │ (로컬 unlock 확정)       │
  │                           │                         │
  │                           │ ③ POST /ISteamUserStats/ │
  │                           │ SetUserStatsForGame      │
  │                           │ ─────────────────────►  │
  │                           │ ◄─────────────────────  │
  │                           │                         │
  │                           │ ④ 성공: steam_synced     │
  │                           │    = true 업데이트       │
  │                           │    실패: Redis ach:retry │
  │                           │    큐에 추가             │
  │                           │                         │
  │ ◄──────────────────────   │                         │
  │ { ok, unlocked_at,        │                         │
  │   steam_synced }          │                         │
  │ (Steam 실패 시             │                         │
  │  steam_synced: false)     │                         │
```

> **저장 순서 원칙**: DB 로컬 업적 기록(②)이 Steam API 호출(③)보다 반드시 먼저 실행된다.  
> Steam API가 실패해도 로컬 unlock은 확정 상태로 유지되며, 큐 재시도로 eventually consistent 달성.

### 업적 재시도 큐 설계
- **주 큐: Redis List** (`ach:retry`) — 백그라운드 워커가 1분 주기로 소비
- **감사 기록: PostgreSQL** `achievement_retry_queue` 테이블 — 재시도 횟수, 실패 원인 영구 보존
- Redis 큐 소비 후 Steam API 성공 시 → DB `steam_synced = true` 업데이트, 감사 테이블 완료 기록

### 핵심 규칙
- 업적 조건 정의는 **서버 코드에만 존재**
- 중복 unlock 요청: `player_achievements` 기록 확인 후 **즉시 200 반환** (멱등)
- 업적 목록 (초기):

| ID | 이름 | 조건 |
|----|------|------|
| `ACH_FIRST_PULL` | 첫 뽑기 | 가챠 1회 이상 |
| `ACH_RARE_UNLOCK` | 희귀 획득 | Rare 이상 아이템 보유 |
| `ACH_LEGENDARY` | 전설의 경지 | Legendary 아이템 보유 |
| `ACH_STREAK_7` | 일주일 수행 | 7일 연속 로그인 |
| `ACH_STREAK_30` | 한 달 수행 | 30일 연속 로그인 |
| `ACH_COLLECTOR` | 수집가 | 스킨 10개 이상 보유 |
