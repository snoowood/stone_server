# Stone Server — API 엔드포인트 설계
> v1.5 — 5차 피드백 반영 (2026-05-03) — 최종 확정

## 공통 규칙

- 기본 URL: `https://{domain}/api/v1`
- 인증: `Authorization: Bearer {JWT}` (🔒 표시된 엔드포인트 필수)
- 응답 형식: JSON
- 오류 형식: `{ "error": "메시지", "code": "ERROR_CODE" }`

---

## 운영 / 헬스체크

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/health` | ❌ | 서버·DB·Redis 상태 확인 |

### GET /health
```
Response (정상):
{ "status": "ok", "db": "ok", "cache": "ok", "version": "1.0.0" }

Response (이상):
HTTP 503
{ "status": "degraded", "db": "ok", "cache": "error" }
```

---

## 인증 (Auth)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| POST | `/auth/steam` | ❌ | Steam 티켓 검증 + JWT 발급 |
| POST | `/auth/refresh` | ❌ | Refresh Token으로 JWT 재발급 |
| DELETE | `/auth/logout` | 🔒 | Redis jti 삭제 → 세션 즉시 무효화 |

### POST /auth/steam

> 클라이언트는 반드시 `GetAuthTicketForWebApi(identity: "stone-server")`로 티켓 발급.  
> `GetAuthSessionTicket()`은 이 엔드포인트와 호환되지 않음.

```
Request:  { "ticket": "hex_encoded_ticket" }
          ※ steam_id는 서버가 Steamworks 검증 결과에서 직접 추출. 클라이언트 전송 불필요.

Response: {
  "jwt": "...",
  "refresh_token": "...",
  "expires_at": "2026-05-04T12:00:00Z"
}

Errors:
  401 INVALID_TICKET   — Steamworks 검증 실패
  409 TICKET_USED      — 동일 티켓 재사용 시도
```

### POST /auth/refresh
```
Request:  { "refresh_token": "..." }
          ※ Authorization 헤더 불필요 (만료된 JWT로 호출하는 경우를 위해)

Response: {
  "jwt": "...",
  "expires_at": "2026-05-04T12:00:00Z"
}
          ※ refresh_token 자체는 재발급하지 않음 (30일 고정)

jti 전환 동작 (Lua 원자적 처리):
  1. refresh:{player_id} 검증 → 불일치 또는 만료 시 401
  2. GET session:current:{player_id} → old_jti 조회
  3. 새 JWT 생성 (new_jti)
  4. Lua 스크립트:
     - DEL session:{old_jti}                          (이전 JWT 즉시 무효화)
     - SET session:{new_jti} TTL 24h                  (새 JWT 등록)
     - SET session:current:{player_id} = new_jti TTL 24h
  5. 새 JWT 반환
  ※ 이 처리 완료 후 이전 JWT로 요청 시 즉시 401 반환됨

Errors:
  401 INVALID_REFRESH_TOKEN  — 존재하지 않거나 만료된 refresh token
  401 REFRESH_TOKEN_REVOKED  — 새 로그인으로 이전 토큰 무효화됨

※ 보안 고려: refresh token은 30일 고정으로 탈취 시 장기 노출 리스크가 있다.
   초기 서비스는 단일 세션 정책으로 범위를 제한하지만,
   추후 **refresh token rotation** (사용 시마다 새 토큰 발급 + 이전 토큰 즉시 폐기) 도입을 검토한다.
   rotation 적용 시 탈취 감지(replayed token → 전체 세션 강제 만료)도 가능해진다.
```

### DELETE /auth/logout
```
Request:  {} (JWT에서 jti 추출)

Response: HTTP 204 No Content

동작 (compare-and-delete):
  1. 요청 JWT에서 jti, player_id 추출
  2. Redis GET session:current:{player_id} → stored_jti
  3. stored_jti == 요청 jti 이면 (현재 활성 세션):
       DEL session:{jti}
       DEL session:current:{player_id}
       DEL refresh:{player_id}
  4. stored_jti != 요청 jti 이면 (이미 새 세션으로 교체된 상태):
       DEL session:{jti} 만 삭제 (새 세션의 current/refresh 보호)
       → 204 정상 응답 (에러 아님)
  ※ 2~4 단계는 Lua 스크립트로 원자적 처리
```

---

## 시간 동기화 (Time)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| POST | `/time/sync` | ❌ | 서버 시간 응답 + drift 확인 |

### POST /time/sync
```
Request:  { "client_timestamp": "2026-05-03T12:00:00Z" }
Response: {
  "server_time": "2026-05-03T12:00:03Z",
  "drift_seconds": 3,
  "warning": false    // drift > 300초(5분)이면 true
}
```

---

## 플레이어 상태 (Player)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/player/state` | 🔒 | 전체 게임 상태 조회 (읽기 전용) |
| POST | `/player/clicks` | 🔒 | 클릭 배치 전송, 포인트 계산 |

> `PATCH /player/state` 범용 수정 API는 존재하지 않는다.  
> 포인트 등 상태 변경은 도메인 이벤트 API(`/player/clicks`, `/gacha/pull`, `/achievement/unlock`)를 통해서만 발생한다.

### GET /player/state
```
Response:
{
  "player_id": "uuid",
  "enlightenment_pts": 1250.5,
  "time_stone_count": 2,
  "streak_days": 7,
  "next_gacha_at": "2026-05-03T13:30:00Z",  // null이면 즉시 가능
  "inventory": [
    { "item_id": "...", "rarity": "rare", "acquired_at": "..." }
  ],
  "updated_at": "2026-05-03T12:00:00Z"
}
```

### POST /player/clicks

> 클릭 어뷰징 방어 규칙: 단일 요청당 count ≤ 300, 시간당 누적 ≤ 3,000, batch_id 중복 제출 차단(5분 창), 연속 요청 간격 ≥ 1초.

```
Request:  { "batch_id": "uuid-v4", "count": 50 }

Response: { "enlightenment_pts": 1300.5 }

Errors:
  400 INVALID_COUNT      — count <= 0 또는 count > 300
  409 DUPLICATE_BATCH    — 동일 batch_id 재전송 (5분 이내)
  429 RATE_EXCEEDED      — 시간당 클릭 상한 초과
  429 TOO_FREQUENT       — 연속 요청 간격 1초 미만
```

> **batch_id 중복 방지 창**: Redis TTL **300초(5분)**. 5분 이내의 동일 batch_id 재전송만 중복으로 감지한다.  
> **⚠️ 5분 초과 패킷 지연으로 인한 동일 batch_id 재도착은 중복으로 감지되지 않아 포인트가 이중 적립될 수 있다.** 이는 현재 서비스 규모에서 수용 가능한 의도적 운영 타협이다. 5분 이상의 지연이 발생하면 네트워크 장애로 간주하고 클라이언트는 새 batch_id로 재시도해야 한다.

---

## 가챠 (Gacha)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| POST | `/gacha/pull` | 🔒 | 가챠 1회 실행 |
| GET | `/gacha/logs` | 🔒 | 가챠 이력 조회 |
| GET | `/gacha/status` | 🔒 | 쿨다운 상태 확인 |

### POST /gacha/pull
```
Request:  {} (JWT에서 player_id 추출)
Response:
{
  "item_id": "skin_legendary_dragon",
  "rarity": "legendary",
  "is_duplicate": false,
  "refund_points": 0,          // 중복 시 환급된 포인트 (is_duplicate=true일 때 양수)
  "next_gacha_at": "2026-05-03T14:00:00Z"
}

Errors:
  409 INSUFFICIENT_POINTS  — 포인트 부족
  429 COOLDOWN_ACTIVE      — { "next_gacha_at": "2026-05-03T13:30:00Z" }
```

### GET /gacha/logs
```
Query:    ?page=1&limit=20
Response: { "logs": [...], "total": 42 }
```

### GET /gacha/status
```
Response: {
  "can_pull": false,
  "next_gacha_at": "2026-05-03T13:30:00Z",
  "pity_count": 5
}
```

---

## 업적 (Achievement)

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| POST | `/achievement/unlock` | 🔒 | 업적 unlock 요청 |
| GET | `/achievement/list` | 🔒 | 내 업적 목록 |

### POST /achievement/unlock

> **비동기 응답 정책**: 로컬 DB unlock(steam_synced=false)은 항상 먼저 확정된다.  
> Steam API 호출 성공 시 steam_synced=true, 실패 시 큐에 적재하고 steam_synced=false로 200 반환.  
> 클라이언트는 steam_synced 값으로 Steam 동기화 상태를 UI에 표시할 수 있다.

```
Request:  { "achievement_id": "ACH_LEGENDARY" }

Response (신규 unlock, Steam 동기화 성공):
HTTP 200
{ "ok": true, "unlocked_at": "2026-05-03T12:05:00Z", "steam_synced": true }

Response (신규 unlock, Steam API 실패 — 큐 적재):
HTTP 200
{ "ok": true, "unlocked_at": "2026-05-03T12:05:00Z", "steam_synced": false }

Response (이미 unlock된 경우 — 멱등 성공):
HTTP 200
{ "ok": true, "already_unlocked": true, "unlocked_at": "2026-04-01T...", "steam_synced": true }

Errors:
  400 CONDITION_NOT_MET  — 업적 조건 미충족
```

### GET /achievement/list
```
Response:
{
  "achievements": [
    {
      "achievement_id": "ACH_FIRST_PULL",
      "name": "첫 뽑기",
      "unlocked": true,
      "unlocked_at": "2026-05-03T10:00:00Z",
      "steam_synced": true
    },
    {
      "achievement_id": "ACH_LEGENDARY",
      "name": "전설의 경지",
      "unlocked": false,
      "unlocked_at": null,
      "steam_synced": false
    }
  ]
}
```

---

## Rate Limit 정책

| 엔드포인트 | 제한 |
|-----------|------|
| `POST /auth/steam` | 10회 / 분 / IP |
| `POST /gacha/pull` | 5회 / 분 / player |
| `POST /player/clicks` | 10회 / 분 / player |
| `POST /achievement/unlock` | 20회 / 분 / player |
| 그 외 | 60회 / 분 / player |

---

## HTTP 상태 코드 사용 규칙

| 코드 | 의미 | 사용처 |
|------|------|--------|
| 200 | OK | 성공 (신규 unlock, 멱등 unlock 모두 포함) |
| 204 | No Content | 로그아웃 성공 |
| 400 | Bad Request | 잘못된 파라미터, 업적 조건 미충족, count 범위 초과 |
| 401 | Unauthorized | JWT 없거나 만료·무효화, Refresh Token 무효 |
| 409 | Conflict | 포인트 부족(`INSUFFICIENT_POINTS`), 티켓/배치 재사용 |
| 429 | Too Many Requests | 가챠 쿨다운, 클릭 상한 초과, Rate Limit |
| 500 | Internal Server Error | 예상치 못한 서버 오류 |
| 503 | Service Unavailable | DB/Redis 연결 불가 |

> `402 Payment Required`는 사용하지 않는다. 포인트 부족은 `409 INSUFFICIENT_POINTS`로 처리.
