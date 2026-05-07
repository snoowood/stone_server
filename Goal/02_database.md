# Stone Server — 데이터베이스 스키마
> v1.5 — 5차 피드백 반영 (2026-05-03) — 최종 확정

## 엔진: PostgreSQL 15

---

## 테이블 목록

| 테이블 | 설명 |
|--------|------|
| `players` | 플레이어 기본 정보 및 실제 로그인 시각 |
| `player_states` | 게임 상태 (포인트, 스트릭 날짜 등) |
| `inventories` | 보유 아이템 목록 |
| `gacha_logs` | 가챠 뽑기 이력 |
| `player_achievements` | 업적 unlock 상태 |
| `achievement_retry_queue` | Steam API 실패 재시도 감사 기록 |

---

## 상세 스키마

### players
```sql
-- last_login: 실제 로그인 일시 (인증 완료 시각 갱신용)
CREATE TABLE players (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    steam_id    VARCHAR(20) UNIQUE NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login  TIMESTAMPTZ       -- 실제 마지막 로그인 일시 (UTC)
);
```

### player_states
```sql
-- last_login_date: 스트릭 판정용 UTC 날짜만 저장 (players.last_login과 역할 다름)
CREATE TABLE player_states (
    player_id           UUID PRIMARY KEY REFERENCES players(id),
    enlightenment_pts   NUMERIC(12, 2) NOT NULL DEFAULT 0,
    time_stone_count    SMALLINT NOT NULL DEFAULT 0 CHECK (time_stone_count <= 3),
    streak_days         INT NOT NULL DEFAULT 0,
    last_login_date     DATE,                       -- UTC 날짜, 스트릭 판정 전용
    next_gacha_at       TIMESTAMPTZ,                -- 가챠 쿨다운 만료 시각 (Redis miss 시 fallback)
    pity_count          INT NOT NULL DEFAULT 0,     -- 연속 비레어 횟수 (pity 확장용)
    -- last_click_batch_id 제거: Redis TTL 300s 창으로 충분히 커버 (단일 값으론 지연 재전송 방어 불충분)
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

> `players.last_login`은 보안/CS용 로그인 시각, `player_states.last_login_date`는 스트릭 계산 전용 날짜. 두 필드는 역할이 다르며 중복이 아니다.

### inventories

**중복 아이템 정책:** 가챠에서 이미 보유한 아이템이 나올 경우 인벤토리에 저장하지 않고 **포인트 환급**으로 처리한다. 따라서 `(player_id, item_id)` UNIQUE 제약을 유지한다.

```sql
CREATE TABLE inventories (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id       UUID NOT NULL REFERENCES players(id),
    item_id         VARCHAR(64) NOT NULL,
    skin_id         VARCHAR(64),
    rarity          VARCHAR(16) NOT NULL,           -- common/uncommon/rare/unique/legendary
    source          VARCHAR(32) NOT NULL,           -- gacha/event/achievement/admin
    acquired_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (player_id, item_id)
);

CREATE INDEX idx_inventories_player ON inventories(player_id);
```

### gacha_logs
```sql
CREATE TABLE gacha_logs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id           UUID NOT NULL REFERENCES players(id),
    item_id             VARCHAR(64) NOT NULL,
    rarity              VARCHAR(16) NOT NULL,
    cost_points         NUMERIC(12, 2) NOT NULL,
    refund_points       NUMERIC(12, 2) NOT NULL DEFAULT 0, -- 중복 환급 시 기록
    gacha_seed_hash     VARCHAR(64) NOT NULL,               -- SHA-256 해시 (원문 저장 안 함)
    pulled_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_gacha_logs_player ON gacha_logs(player_id, pulled_at DESC);
```

> `gacha_seed_hash`: 원문 시드 저장 시 외부 유출 시 확률 추론 가능. SHA-256 해시만 저장하여 감사 목적(동일 시드 재사용 탐지)만 충족한다.

### player_achievements
```sql
CREATE TABLE player_achievements (
    player_id       UUID NOT NULL REFERENCES players(id),
    achievement_id  VARCHAR(64) NOT NULL,
    unlocked_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    steam_synced    BOOLEAN NOT NULL DEFAULT FALSE,

    PRIMARY KEY (player_id, achievement_id)
);

CREATE INDEX idx_player_achievements_player ON player_achievements(player_id);
```

### achievement_retry_queue
```sql
-- 주 큐: Redis ach:retry List (실시간 처리)
-- 이 테이블: 감사/영구 기록 전용 (재시도 이력, 실패 원인 추적)
CREATE TABLE achievement_retry_queue (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id       UUID NOT NULL REFERENCES players(id),
    achievement_id  VARCHAR(64) NOT NULL,
    retry_count     INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_retry_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ,                    -- NULL이면 미처리
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ach_retry_next ON achievement_retry_queue(next_retry_at)
    WHERE resolved_at IS NULL;
```

---

## Redis 키 설계

| 키 패턴 | 타입 | TTL | 용도 |
|---------|------|-----|------|
| `session:{jti}` | String | 24h | JWT 유효성 검증 (로그아웃 시 삭제) |
| `session:current:{player_id}` | String (jti) | 24h | 현재 활성 jti 추적 (새 로그인/로그아웃 경합 처리용) |
| `refresh:{player_id}` | String (refresh_token) | 30d | Refresh Token (플레이어당 1개, 새 로그인 시 교체) |
| `gacha:cooldown:{player_id}` | String (timestamp) | 30m | 가챠 쿨다운 (`next_gacha_at`, Redis miss 시 DB fallback) |
| `ticket:used:{ticket_hash}` | String | 1h | Steam 티켓 재사용 방지 |
| `ratelimit:{player_id}:{endpoint}` | Counter | 1m | Rate Limiting |
| `click:batch:{player_id}:{batch_id}` | String | 300s | 클릭 배치 중복 제출 방지 (5분 anti-replay 창, **5분 초과 재도착은 중복 미감지 — 의도적 허용**) |
| `click:hourly:{player_id}` | Counter | 1h | 시간당 클릭 상한 추적 |
| `ach:retry` | List | - | 업적 Steam API 재시도 주 큐 |

### 단일 세션 강제 로직 (새 로그인)
```
1. session:current:{player_id} 에서 이전 jti 조회
2. 이전 jti 존재하면 session:{이전jti} 삭제 (강제 무효화)
3. 새 jti를 session:{jti} 와 session:current:{player_id} 에 저장
4. refresh:{player_id} 갱신 (이전 refresh token 자동 무효화)
```

### Refresh Token 갱신 시 jti 전환 (POST /auth/refresh)
```
1. refresh:{player_id} 검증 (없거나 불일치 → 401)
2. GET session:current:{player_id} → old_jti
3. 새 JWT 발급 (new_jti)
4. Lua 스크립트 원자적 처리:
   - DEL session:{old_jti}
   - SET session:{new_jti} TTL 24h
   - SET session:current:{player_id} = new_jti TTL 24h
5. 새 JWT 반환 (refresh_token 재발급 안 함)
```
> 이 원자적 처리로 refresh 이후 이전 JWT로 요청 시 즉시 401 반환. Lua로 GET→DEL→SET이 단일 트랜잭션으로 처리됨.

### 로그아웃 경합 처리 (compare-and-delete)
```
1. 요청 JWT의 jti 추출
2. GET session:current:{player_id} → stored_jti
3. stored_jti == 요청 jti 이면:
     DEL session:{jti}, session:current:{player_id}, refresh:{player_id}
4. stored_jti != 요청 jti 이면:
     DEL session:{jti} 만 삭제 (새 세션 보호)
     → 204 정상 응답
```
> 원자성 보장을 위해 Lua 스크립트로 GET→비교→DEL을 단일 트랜잭션으로 처리한다.

---

## 인덱스 전략 (전체)

| 테이블 | 인덱스 | 이유 |
|--------|--------|------|
| `players` | `steam_id` UNIQUE | 로그인마다 조회 |
| `inventories` | `(player_id, item_id)` UNIQUE | 중복 방지 + 보유 확인 |
| `inventories` | `player_id` | 인벤토리 목록 조회 |
| `gacha_logs` | `(player_id, pulled_at DESC)` | 이력 페이징 |
| `player_achievements` | `(player_id, achievement_id)` PK | 조건 확인 |
| `player_achievements` | `player_id` | 업적 목록 조회 |
| `achievement_retry_queue` | `next_retry_at` WHERE `resolved_at IS NULL` | 워커 스캔 |

---

## 마이그레이션 방식

- `migrations/` 폴더에 번호 순서 SQL 파일 관리
  - `001_create_players.sql`
  - `002_create_player_states.sql`
  - `003_create_inventories.sql`
  - `004_create_gacha_logs.sql`
  - `005_create_player_achievements.sql`
  - `006_create_achievement_retry_queue.sql`
  - (007 삭제 — `last_click_batch_id` 컬럼 불필요, Redis TTL 300s로 대체)
- `golang-migrate` 라이브러리 사용 (서버 시작 시 자동 적용)
