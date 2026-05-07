# Stone Server — 구현 작업 목록
> v1.0 (2026-05-03)

## 문서 규칙

- **Task ID**: `T{Phase번호}-{순번}` (예: T1-03)
- **선행 의존**: 이 Task 시작 전 완료되어야 하는 Task
- **후행 연계**: 이 Task 완료 후 잠금 해제되는 Task
- **리뷰 목표**: Task 완료 판정 기준 (모두 통과해야 완료)
- Server 트랙 작업만 수록. Client(Unity) 트랙은 `04_milestones.md` 참조.

---

## Phase 1 — 인프라 + 인증 기반

> 목표: 서버가 실제로 뜨고, Steam 로그인이 작동하며, 기본 운영 도구가 갖춰진 상태

---

### T1-01 — Go 프로젝트 초기화

**의도**
모든 코드 작업의 기반이 되는 Go 모듈 구조와 빌드 환경을 잡는다.
이후 모든 Task는 여기서 확립된 디렉토리 구조와 빌드 파이프라인 위에서 진행된다.

**작업 목표**
- `go mod init` 으로 모듈 초기화 (`github.com/gensdeis/stone-server`)
- `00_overview.md` 기준 디렉토리 구조 생성
  - `cmd/server/main.go`, `internal/auth`, `internal/gacha`, `internal/player`, `internal/timeguard`, `internal/achievement`, `pkg/db`, `pkg/cache`, `migrations/`
- `Dockerfile` 작성 (멀티스테이지 빌드, 최종 이미지 scratch 또는 alpine)
- `go build ./...` 성공 확인

**선행 의존**: 없음

**후행 연계**: T1-02, T1-03

**리뷰 목표**
- [ ] `go build ./...` 오류 없이 통과
- [ ] `00_overview.md` 디렉토리 구조와 일치
- [ ] Dockerfile 빌드 성공, 불필요한 파일 이미지에 미포함 확인

---

### T1-02 — 환경변수 관리 + 구조화 로깅

**의도**
민감값(DB 비밀번호, Steam API 키, JWT 서명키 등)이 코드에 절대 하드코딩되지 않도록 기반을 잡는다.
이후 모든 Task에서 로그와 설정값을 일관되게 사용하기 위해 초기에 확립한다.

**작업 목표**
- `.env.example` 파일 작성 (DB_URL, REDIS_URL, STEAM_API_KEY, JWT_PRIVATE_KEY 등)
- `os.Getenv` 또는 경량 라이브러리(`godotenv`)로 `.env` 로드
- `zerolog` 적용, 요청 미들웨어에서 `player_id`, `request_id`, `method`, `path`, `status`, `latency` 로깅
- 민감값은 로그에 절대 출력되지 않는 구조 확인

**선행 의존**: T1-01

**후행 연계**: T1-03, T1-05, T1-07

**리뷰 목표**
- [ ] `.env` 없이 실행 시 명확한 오류 메시지 출력 후 종료
- [ ] 요청 로그에 `player_id`, `status`, `latency` 포함 확인
- [ ] `JWT_PRIVATE_KEY`, `DB_PASSWORD` 등 민감값이 로그에 미출력
- [ ] `.env` 파일이 `.gitignore`에 포함

---

### T1-03 — Docker Compose 구성

**의도**
Go App + PostgreSQL + Redis + Nginx를 단일 호스트에서 함께 실행하는 컨테이너 환경을 구성한다.
로컬 개발과 프로덕션 배포에 동일한 Compose 파일을 사용하여 환경 차이를 최소화한다.

**작업 목표**
- `docker-compose.yml` 작성
  - `app`: Go 서버 (`:8080`)
  - `postgres`: PostgreSQL 15 (`:5432`, volume 마운트)
  - `redis`: Redis 7 (`:6379`)
  - `nginx`: Nginx (`:443 → app:8080`, SSL 설정 분리)
- 서비스 간 헬스체크 및 `depends_on` 설정
- PostgreSQL 데이터 볼륨 영속성 확인

**선행 의존**: T1-01

**후행 연계**: T1-04, T1-05, T1-10, T1-11

**리뷰 목표**
- [ ] `docker-compose up` 후 4개 서비스 모두 정상 실행
- [ ] `postgres` 볼륨 재시작 후 데이터 보존 확인
- [ ] `app` → `postgres`, `app` → `redis` 네트워크 통신 확인
- [ ] `nginx` → `app:8080` 프록시 확인

---

### T1-04 — DB 마이그레이션 연결 (001–002)

**의도**
`golang-migrate`를 서버 시작 시 자동 적용되도록 연결하여, 스키마 변경을 코드와 함께 버전 관리한다.
Phase 1 범위인 `players`, `player_states` 테이블을 생성한다.

**작업 목표**
- `pkg/db` 패키지에 PostgreSQL 연결 풀 (`pgxpool`) 초기화 코드 작성
- `golang-migrate` 연결, `cmd/server/main.go` 시작 시 자동 `Up()` 호출
- `migrations/001_create_players.sql` 작성 (`players` 스키마 기준, `02_database.md` 참조)
- `migrations/002_create_player_states.sql` 작성 (`player_states` 스키마 기준)
- 각 마이그레이션 파일에 대응하는 `*.down.sql` 작성

**선행 의존**: T1-03

**후행 연계**: T1-05, T1-07

**리뷰 목표**
- [ ] 서버 시작 시 마이그레이션 자동 적용 로그 출력
- [ ] `players`, `player_states` 테이블 및 제약조건 스키마 일치 확인
- [ ] `down` 마이그레이션 적용 후 테이블 정상 삭제 확인
- [ ] DB 연결 실패 시 서버 시작 중단 확인

---

### T1-05 — GET /health 구현

**의도**
외부(로드밸런서, 모니터링 도구, 운영자)가 서버 상태를 확인할 수 있는 단일 진입점을 만든다.
DB·Redis 연결 상태를 실시간으로 포함하여 장애 위치를 빠르게 파악할 수 있도록 한다.

**작업 목표**
- `GET /api/v1/health` 엔드포인트 구현
- DB `ping`, Redis `ping` 을 각각 검사하여 결과를 응답에 포함
- 정상: `200 { status: ok, db: ok, cache: ok, version: "1.0.0" }`
- 이상: `503 { status: degraded, db: ok, cache: error }` (한 가지라도 실패 시)

**선행 의존**: T1-02, T1-03, T1-04

**후행 연계**: T1-11 (배포 후 외부 접속 검증)

**리뷰 목표**
- [ ] DB·Redis 모두 정상 시 200 응답
- [ ] DB 연결 끊은 상태에서 503 + `db: error` 응답
- [ ] Redis 연결 끊은 상태에서 503 + `cache: error` 응답
- [ ] 인증 미들웨어 없이 호출 가능 확인 (공개 엔드포인트)

---

### T1-06 — Rate Limiting 미들웨어

**의도**
API 남용 및 무차별 대입 공격을 방지한다.
인증 구현 이전에 공통 미들웨어로 먼저 자리잡아, 이후 인증 API에 자동으로 적용되도록 한다.

**작업 목표**
- Redis `INCR` + `EXPIRE` 기반 슬라이딩 윈도우 rate limit 구현
- `03_api.md` Rate Limit 정책 적용
  - `POST /auth/steam`: 10회/분/IP
  - `POST /gacha/pull`: 5회/분/player
  - `POST /player/clicks`: 10회/분/player
  - `POST /achievement/unlock`: 20회/분/player
  - 그 외: 60회/분/player
- 초과 시 `429 Too Many Requests` 반환
- Redis 키: `ratelimit:{player_id 또는 IP}:{endpoint}`

**선행 의존**: T1-03

**후행 연계**: T1-07, T2-04, T3-02, T4-03

**리뷰 목표**
- [ ] 제한 횟수 초과 시 429 응답
- [ ] 1분 경과 후 카운터 리셋 확인
- [ ] 인증 전 엔드포인트(auth/steam)는 IP 기준, 인증 후는 player_id 기준 적용 확인

---

### T1-07 — POST /auth/steam 구현

**의도**
Steam 티켓을 Steamworks Web API로 검증하고 JWT를 발급하는 인증 핵심 로직을 구현한다.
steam_id는 반드시 Steamworks 검증 결과에서만 추출하여 클라이언트 위조를 차단한다.

**작업 목표**
- `ISteamUserAuth/AuthenticateUserTicket` Web API 호출 구현
- 검증 결과 `steam_id` 추출 → `players` 테이블 upsert
- `player_states` 신규 플레이어 초기 레코드 생성
- JWT(RS256) 생성, jti(UUID) 포함
- Redis: `session:{jti}` TTL 24h, `session:current:{player_id}` TTL 24h 저장
- 신규 로그인 시 이전 jti(`session:current` 조회) 삭제 → 단일 세션 강제
- Refresh Token 생성 → Redis `refresh:{player_id}` TTL 30d 저장
- 티켓 해시 → Redis `ticket:used:{hash}` TTL 1h 저장 (재사용 차단)
- `players.last_login` 갱신

**선행 의존**: T1-02, T1-04, T1-06

**후행 연계**: T1-08, T1-09

**리뷰 목표**
- [ ] 유효 티켓 → `{ jwt, refresh_token, expires_at }` 수신
- [ ] 동일 티켓 재사용 → `409 TICKET_USED`
- [ ] 무효 티켓 → `401 INVALID_TICKET`
- [ ] 새 로그인 후 이전 세션 jti Redis에서 삭제 확인
- [ ] `players` 테이블 upsert 정상 동작 확인
- [ ] `player_states` 신규 레코드 자동 생성 확인

---

### T1-08 — JWT 미들웨어 구현

**의도**
🔒 표시된 모든 엔드포인트에 일관된 인증 게이트를 적용한다.
서명 검증만으로는 로그아웃된 세션을 차단할 수 없으므로 Redis jti 검증을 반드시 병행한다.

**작업 목표**
- `Authorization: Bearer {JWT}` 헤더 파싱
- JWT RS256 서명 검증
- Redis `session:{jti}` 존재 여부 확인 (없으면 401)
- `player_id`, `jti` 를 Gin Context에 주입 (이후 핸들러에서 사용)

**선행 의존**: T1-07

**후행 연계**: T1-09, T2-03, T2-04, T3-02, T4-03

**리뷰 목표**
- [ ] 유효 JWT + Redis jti 존재 → 통과
- [ ] 만료된 JWT → `401`
- [ ] Redis에서 jti 수동 삭제 후 유효 JWT 요청 → `401` (로그아웃 무효화 확인)
- [ ] Authorization 헤더 없는 요청 → `401`

---

### T1-09 — POST /auth/refresh 구현

**의도**
만료된 JWT를 새로 발급하되, 이전 JWT를 즉시 무효화하여 단일 활성 세션 원칙을 유지한다.
old_jti 삭제와 new_jti 등록이 원자적으로 처리되지 않으면 세션 상태가 어긋날 수 있다.

**작업 목표**
- `refresh:{player_id}` 검증 (없거나 불일치 → 401)
- `GET session:current:{player_id}` → `old_jti` 조회
- 새 JWT 생성 (`new_jti`)
- Lua 스크립트로 원자적 처리:
  - `DEL session:{old_jti}`
  - `SET session:{new_jti}` TTL 24h
  - `SET session:current:{player_id} = new_jti` TTL 24h
- 새 JWT 반환 (refresh_token 재발급 없음)

**선행 의존**: T1-07, T1-08

**후행 연계**: T1-10

**리뷰 목표**
- [ ] 정상 refresh_token → 새 JWT 발급
- [ ] refresh 후 이전 JWT로 요청 → `401` 즉시
- [ ] refresh 후 새 JWT로 요청 → `200` 정상
- [ ] 무효/만료 refresh_token → `401 INVALID_REFRESH_TOKEN`
- [ ] Lua 스크립트 원자성 확인 (단위 테스트)

---

### T1-10 — DELETE /auth/logout 구현

**의도**
세션을 즉시 무효화하되, 새 로그인과 구 세션 로그아웃이 경합하는 상황에서 신규 세션을 보호한다.
compare-and-delete 없이 구현하면 로그아웃이 방금 로그인한 새 세션을 삭제할 수 있다.

**작업 목표**
- JWT에서 `jti`, `player_id` 추출
- Lua 스크립트로 원자적 처리:
  - `GET session:current:{player_id}` → `stored_jti`
  - `stored_jti == 요청 jti`: `DEL session:{jti}`, `DEL session:current:{player_id}`, `DEL refresh:{player_id}`
  - `stored_jti != 요청 jti`: `DEL session:{jti}` 만 삭제 (새 세션 current/refresh 보호)
- 양 경우 모두 `204 No Content` 반환

**선행 의존**: T1-08, T1-09

**후행 연계**: 없음 (Phase 1 최종 인증 구현)

**리뷰 목표**
- [ ] 현재 세션 로그아웃 → 204, 이후 동일 JWT 재사용 → 401
- [ ] 새 기기 로그인 직후 구 세션으로 로그아웃 → 204, 새 세션 current/refresh 보호 확인
- [ ] Lua 스크립트 원자성 단위 테스트 통과
- [ ] JWT 없는 로그아웃 요청 → 401 (JWT 미들웨어 차단)

---

### T1-11 — AWS Lightsail + SSL 세팅

**의도**
코드가 완성된 서버를 실제 프로덕션 환경에 배포하고 HTTPS로 외부 접속을 검증한다.
이 Task 완료 후 Unity 클라이언트가 실제 서버를 바라볼 수 있게 된다.

**작업 목표**
- AWS Lightsail 서울 리전 $20 플랜 인스턴스 생성 (4GB/2vCPU)
- SSH 접속 확인, Docker + Docker Compose 설치
- 도메인 연결 (Lightsail DNS 또는 외부 DNS)
- Let's Encrypt Certbot으로 SSL 인증서 발급
- Nginx 443 → app:8080 SSL 종료 설정
- `docker-compose up -d` 로 서비스 기동
- 외부에서 `GET https://{domain}/api/v1/health` 호출 확인

**선행 의존**: T1-05, T1-10

**후행 연계**: Phase 2 전체 (외부 접속 가능 환경 기반)

**리뷰 목표**
- [ ] HTTPS `GET /health` 외부 정상 응답
- [ ] HTTP → HTTPS 리다이렉트 확인
- [ ] SSL 인증서 유효기간 확인 (90일, 자동 갱신 설정)
- [ ] PostgreSQL, Redis 외부 직접 접속 불가 확인 (방화벽)
- [ ] `.env` 파일 서버에서 git에 미포함 확인

---

## Phase 2 — 시간 검증 + 플레이어 상태 동기화

> 목표: 게임 상태를 서버에서 관리하고, 시간 어뷰징 + 클릭 어뷰징 차단

---

### T2-01 — DB 마이그레이션 (003–004)

**의도**
가챠 로그와 인벤토리 테이블을 미리 생성한다.
Phase 2의 `GET /player/state`가 인벤토리를 포함하여 응답해야 하므로 Phase 2 시작 시점에 필요하다.

**작업 목표**
- `migrations/003_create_inventories.sql` (`inventories` 스키마, UNIQUE 제약 포함)
- `migrations/004_create_gacha_logs.sql` (`gacha_logs` 스키마, 인덱스 포함)
- 대응하는 `*.down.sql` 작성

**선행 의존**: Phase 1 완료

**후행 연계**: T2-03, T3-02

**리뷰 목표**
- [ ] 마이그레이션 자동 적용 확인
- [ ] `inventories` UNIQUE `(player_id, item_id)` 제약 확인
- [ ] `gacha_logs` `(player_id, pulled_at DESC)` 인덱스 확인

---

### T2-02 — POST /time/sync 구현

**의도**
클라이언트가 시스템 시계를 조작하여 가챠 쿨다운이나 일일 스트릭을 우회하는 것을 감지한다.
강제 차단은 하지 않고 경고를 반환하여 클라이언트가 UI로 사용자에게 알릴 수 있도록 한다.

**작업 목표**
- `POST /api/v1/time/sync` 구현 (인증 불필요)
- `client_timestamp` 파싱 (RFC3339)
- `drift = |server_time - client_timestamp|` 계산
- `drift > 300초`이면 `warning: true`
- 응답: `{ server_time, drift_seconds, warning }`

**선행 의존**: Phase 1 완료

**후행 연계**: 없음 (독립 기능)

**리뷰 목표**
- [ ] 서버-클라이언트 시간 정상 범위 → `warning: false`
- [ ] 5분 이상 차이 → `warning: true`, 올바른 `drift_seconds` 값
- [ ] 인증 헤더 없이 호출 가능 확인

---

### T2-03 — GET /player/state 구현

**의도**
클라이언트가 로그인 시 서버 상태를 전체 로드하여 로컬 JSON 의존을 제거한다.
이 API가 Source of Truth 역할을 하므로 모든 상태 필드를 정확히 반환해야 한다.

**작업 목표**
- `GET /api/v1/player/state` 구현 (JWT 인증 필요)
- `players`, `player_states` JOIN 조회
- `inventories` WHERE `player_id` 조회 포함
- `next_gacha_at` null이면 즉시 가챠 가능 표시
- `03_api.md` 응답 스펙 그대로 반환

**선행 의존**: T2-01, T1-08

**후행 연계**: T2-04

**리뷰 목표**
- [ ] 전체 상태 필드 (`enlightenment_pts`, `time_stone_count`, `streak_days`, `next_gacha_at`, `inventory`) 정상 반환
- [ ] 빈 인벤토리 플레이어 → `inventory: []` 정상 반환
- [ ] 인증 없는 요청 → 401

---

### T2-04 — POST /player/clicks 기본 구현

**의도**
클릭 포인트 계산을 서버에서 처리하여 클라이언트가 임의 값을 보낼 수 없도록 한다.
서버가 `count × pointsPerClick`을 계산하고 DB에 반영한 뒤 최신 포인트를 반환한다.

**작업 목표**
- `POST /api/v1/player/clicks` 구현 (JWT 인증 필요)
- 요청: `{ batch_id: uuid-v4, count: N }`
- `pointsPerClick` 서버 설정값 기반 계산
- `player_states.enlightenment_pts` 업데이트 (DB)
- 응답: `{ enlightenment_pts: 새 포인트 값 }`

**선행 의존**: T2-03

**후행 연계**: T2-05

**리뷰 목표**
- [ ] 정상 요청 → 포인트 증가 후 반환
- [ ] DB `enlightenment_pts` 실제 반영 확인
- [ ] `batch_id` 없는 요청 → 400

---

### T2-05 — 클릭 어뷰징 방어 구현

**의도**
클릭 이벤트가 포인트 수급의 주요 경로이므로, 조작된 클라이언트의 비정상 요청을 서버에서 차단한다.
4개 규칙이 각각 독립적으로 동작하며 모두 통과해야 포인트가 지급된다.

**작업 목표**
- **batch_id 중복 방지**: Redis `click:batch:{player_id}:{batch_id}` TTL 300s 확인 → 존재하면 `409 DUPLICATE_BATCH`
- **count 상한**: `count ≤ 0` 또는 `count > 300` → `400 INVALID_COUNT`
- **시간당 상한**: Redis `click:hourly:{player_id}` INCR + 누적 > 3000 → `429 RATE_EXCEEDED`
- **연속 요청 간격**: 마지막 요청 시각 기록, 간격 < 1초 → `429 TOO_FREQUENT`
- T2-04 처리 전 위 검증을 모두 선행

**선행 의존**: T2-04

**후행 연계**: 없음 (Phase 2 최종)

**리뷰 목표**
- [ ] 동일 batch_id 5분 이내 재전송 → `409 DUPLICATE_BATCH`
- [ ] 동일 batch_id 5분 이후 재전송 → 새 배치로 정상 처리 (의도적 허용 범위 확인)
- [ ] `count = 0` → `400 INVALID_COUNT`
- [ ] `count = 301` → `400 INVALID_COUNT`
- [ ] 시간당 3000 초과 → `429 RATE_EXCEEDED`
- [ ] 1초 미만 연속 요청 → `429 TOO_FREQUENT`
- [ ] 단위 테스트: 4개 규칙 각각 + 조합 케이스 통과

---

## Phase 3 — 가챠 시스템 서버화

> 목표: 가챠 결과를 서버에서 결정하고 쿨다운을 강제 적용

---

### T3-01 — 가챠 RNG + 확률 테이블 구현

**의도**
가챠 결과 결정 로직을 서버에 격리한다.
확률 테이블이 클라이언트에 노출되면 역산 가능하므로 서버 코드에만 존재해야 한다.
감사 목적으로 원문 시드 대신 SHA-256 해시만 저장한다.

**작업 목표**
- `crypto/rand` 기반 RNG 구현 (예측 불가능)
- 확률 테이블 서버 코드 정의:
  - Common 60% / Uncommon 30% / Rare 9% / Unique 0.9% / Legendary 0.1%
- 시드 SHA-256 해시 생성 (원문 미저장)
- 결과 아이템 ID 및 rarity 반환 함수
- 환급 포인트 테이블 (rarity별 고정값, GameConfig 형태로 서버 설정)

**선행 의존**: Phase 2 완료

**후행 연계**: T3-02

**리뷰 목표**
- [ ] 대량 호출(1만 회) 시 확률 분포가 설계값과 ±2% 이내
- [ ] `crypto/rand` 사용 확인 (`math/rand` 미사용)
- [ ] `gacha_seed_hash` SHA-256 형식 저장, 원문 미포함 확인
- [ ] 단위 테스트: 각 rarity 반환 케이스 커버

---

### T3-02 — POST /gacha/pull 구현

**의도**
가챠 1회 실행의 모든 부작용(포인트 차감, 인벤토리, 로그, 쿨다운)을 단일 트랜잭션으로 묶어 중간 실패 시 롤백을 보장한다.
트랜잭션 외부에서 Redis 쿨다운을 관리하여 DB 부하 없이 빠르게 쿨다운을 확인한다.

**작업 목표**
- 쿨다운 확인: `Redis gacha:cooldown:{player_id}` 조회 → miss 시 DB `player_states.next_gacha_at` fallback
- 쿨다운 중이면 `429 COOLDOWN_ACTIVE + { next_gacha_at }` 반환
- **단일 트랜잭션**:
  1. `player_states.enlightenment_pts` 잔액 확인 → 부족 시 `409 INSUFFICIENT_POINTS`
  2. 포인트 차감
  3. T3-01 RNG로 결과 결정
  4. `inventories` UPSERT 시도 → 중복 시 인벤토리 미저장 + 환급 포인트 계산
  5. `player_states.pity_count` 업데이트
  6. `gacha_logs` INSERT (`gacha_seed_hash`, `refund_points` 포함)
  7. `player_states.next_gacha_at` 업데이트
- COMMIT 후 Redis `gacha:cooldown:{player_id}` 갱신 (TTL 30m)
- 응답: `{ item_id, rarity, is_duplicate, refund_points, next_gacha_at }`

**선행 의존**: T3-01, T2-01

**후행 연계**: T3-03, T3-04

**리뷰 목표**
- [ ] 쿨다운 중 요청 → `429 COOLDOWN_ACTIVE + next_gacha_at`
- [ ] 포인트 부족 → `409 INSUFFICIENT_POINTS`
- [ ] 중복 아이템 → `is_duplicate: true`, `refund_points` 양수, 인벤토리 미추가
- [ ] 트랜잭션 중간 실패 시 포인트·인벤토리·로그 모두 롤백 (단위 테스트)
- [ ] Redis miss 시 DB fallback으로 쿨다운 복구 확인
- [ ] `gacha_logs` 기록 정상 확인

---

### T3-03 — GET /gacha/status 구현

**의도**
클라이언트가 가챠 버튼 활성화 여부를 서버에서 확인할 수 있도록 한다.
Redis와 DB 양쪽을 일관되게 조회하여 쿨다운 상태를 정확히 반환한다.

**작업 목표**
- `GET /api/v1/gacha/status` 구현 (JWT 인증 필요)
- Redis `gacha:cooldown:{player_id}` 조회 → miss 시 DB fallback
- `can_pull`, `next_gacha_at`, `pity_count` 반환

**선행 의존**: T3-02

**후행 연계**: 없음 (독립 조회)

**리뷰 목표**
- [ ] 쿨다운 중 → `can_pull: false + next_gacha_at`
- [ ] 쿨다운 없음 → `can_pull: true + next_gacha_at: null`
- [ ] Redis 키 수동 삭제 후 DB fallback 동작 확인

---

### T3-04 — GET /gacha/logs 구현

**의도**
플레이어가 자신의 가챠 이력을 확인할 수 있도록 한다.
대량 데이터 처리를 위해 페이징을 적용하여 DB 부하를 제한한다.

**작업 목표**
- `GET /api/v1/gacha/logs?page=1&limit=20` 구현 (JWT 인증 필요)
- `gacha_logs` WHERE `player_id` ORDER BY `pulled_at DESC` 페이징 쿼리
- 응답: `{ logs: [...], total: N }`

**선행 의존**: T3-02

**후행 연계**: 없음

**리뷰 목표**
- [ ] 페이징 정상 동작 (`page`, `limit` 파라미터)
- [ ] `pulled_at DESC` 정렬 확인
- [ ] `total` 값 정확성 확인
- [ ] `limit` 상한 없을 시 기본값 적용 확인

---

## Phase 4 — Steam 업적 시스템

> 목표: 조건 검증 후 서버에서 Steam 업적 unlock

---

### T4-01 — DB 마이그레이션 (005–006)

**의도**
업적 unlock 상태와 Steam API 재시도 감사 기록을 저장할 테이블을 생성한다.
`achievement_retry_queue`는 Redis 큐 실패 시 감사 목적으로만 사용하며 주 큐는 Redis다.

**작업 목표**
- `migrations/005_create_player_achievements.sql` (PK: `player_id + achievement_id`)
- `migrations/006_create_achievement_retry_queue.sql` (partial index 포함)
- 대응하는 `*.down.sql` 작성

**선행 의존**: Phase 3 완료

**후행 연계**: T4-02, T4-03, T4-04, T4-05

**리뷰 목표**
- [ ] 마이그레이션 적용 확인
- [ ] `player_achievements` PK 복합키 제약 확인
- [ ] `achievement_retry_queue` partial index (`WHERE resolved_at IS NULL`) 확인

---

### T4-02 — 업적 조건 정의 (6개)

**의도**
업적 조건 로직을 서버 코드에만 존재하게 하여 클라이언트 위조를 차단한다.
각 업적 조건은 DB 쿼리로 검증하여 현재 상태를 기준으로 판단한다.

**작업 목표**
- `internal/achievement/conditions.go` 에 6개 업적 조건 구현:
  - `ACH_FIRST_PULL`: `gacha_logs` WHERE `player_id` COUNT ≥ 1
  - `ACH_RARE_UNLOCK`: `inventories` WHERE `player_id AND rarity IN ('rare','unique','legendary')` COUNT ≥ 1
  - `ACH_LEGENDARY`: `inventories` WHERE `player_id AND rarity = 'legendary'` COUNT ≥ 1
  - `ACH_STREAK_7`: `player_states.streak_days ≥ 7`
  - `ACH_STREAK_30`: `player_states.streak_days ≥ 30`
  - `ACH_COLLECTOR`: `inventories` WHERE `player_id` COUNT ≥ 10
- 조건 미충족 시 `400 CONDITION_NOT_MET` 반환용 에러 정의

**선행 의존**: T4-01

**후행 연계**: T4-03

**리뷰 목표**
- [ ] 각 업적 조건 충족/미충족 케이스 단위 테스트 통과
- [ ] 조건 로직이 서버 코드에만 존재 (클라이언트 전달 없음)
- [ ] DB 쿼리 각각 실행 계획 확인 (인덱스 활용)

---

### T4-03 — POST /achievement/unlock 구현

**의도**
업적 unlock을 DB에 먼저 확정한 뒤 Steam API를 호출하여, Steam API 장애 시에도 로컬 unlock이 유지되는 eventually consistent 구조를 구현한다.

**작업 목표**
- `POST /api/v1/achievement/unlock` 구현 (JWT 인증 필요)
- 멱등 처리: `player_achievements` 이미 존재 시 `200 { ok: true, already_unlocked: true }` 즉시 반환
- T4-02 조건 검증 → 미충족 시 `400 CONDITION_NOT_MET`
- **DB 먼저**: `player_achievements` INSERT (`steam_synced = false`)
- Steamworks `SetUserStatsForGame` API 호출
  - 성공: `steam_synced = true` 업데이트
  - 실패: Redis `ach:retry` LPUSH + `achievement_retry_queue` INSERT (감사 기록)
- 응답: `{ ok, unlocked_at, steam_synced }`

**선행 의존**: T4-02, T1-08

**후행 연계**: T4-05

**리뷰 목표**
- [ ] 조건 충족 → `200 { ok: true, steam_synced: true }`
- [ ] 동일 업적 재요청 → `200 { ok: true, already_unlocked: true }`
- [ ] 조건 미충족 → `400 CONDITION_NOT_MET`
- [ ] Steam API 실패 → `200 { steam_synced: false }` + Redis 큐 적재 확인
- [ ] DB INSERT가 Steam API 호출보다 먼저 실행됨 단위 테스트로 확인
- [ ] `player_achievements` 레코드 먼저 커밋 확인 (Steam API 실패 후에도 DB에 존재)

---

### T4-04 — GET /achievement/list 구현

**의도**
플레이어가 자신의 업적 달성 현황과 Steam 동기화 상태를 확인할 수 있도록 한다.

**작업 목표**
- `GET /api/v1/achievement/list` 구현 (JWT 인증 필요)
- 서버에 정의된 전체 6개 업적 목록 기준으로 응답 생성
- `player_achievements` JOIN으로 unlocked 여부 결합
- `unlocked_at: null`, `steam_synced: false` 기본값 처리

**선행 의존**: T4-01, T1-08

**후행 연계**: 없음

**리뷰 목표**
- [ ] 전체 6개 업적 항목 반환 (미달성 포함)
- [ ] 달성 업적 `unlocked: true`, `unlocked_at` 값 확인
- [ ] 미달성 업적 `unlocked: false`, `unlocked_at: null` 확인
- [ ] `steam_synced` 값 정확 반환

---

### T4-05 — 업적 재시도 백그라운드 워커

**의도**
Steam API 장애 시 큐에 적재된 업적 unlock 요청을 복구 후 자동으로 재처리하여 eventual consistency를 달성한다.

**작업 목표**
- Go goroutine + ticker 기반 1분 주기 워커 구현
- Redis `ach:retry` RPOP으로 항목 소비
- Steamworks API 재호출
  - 성공: `player_achievements.steam_synced = true`, `achievement_retry_queue.resolved_at` 업데이트
  - 실패: 항목 다시 LPUSH, `retry_count` + 1, `last_error` 기록
- 워커 graceful shutdown (서버 종료 시 진행 중 작업 완료 대기)

**선행 의존**: T4-03

**후행 연계**: 없음 (Phase 4 최종)

**리뷰 목표**
- [ ] Steam API 실패 후 1분 이내 재시도 동작 확인
- [ ] 재시도 성공 후 `steam_synced = true` 업데이트 확인
- [ ] 서버 재시작 후 큐 잔여 항목 재처리 확인
- [ ] 워커 panic 시 서버 전체 중단 없이 재기동 확인

---

## Phase 5 — 운영 안정화

> 목표: 라이브 배포 전 운영 필수 항목 완비

---

### T5-01 — 자동 DB 백업

**의도**
단일 호스트 구조의 리스크를 완화하기 위해 RPO(최대 24시간 데이터 손실 허용) 기준을 만족하는 자동 백업을 구성한다.

**작업 목표**
- 일 1회 `pg_dump` 실행 스크립트 작성
- 백업 파일 7일치 보관 (8일차 자동 삭제)
- cron 또는 systemd timer 등록
- 백업 파일 외부 저장소 이전 검토 (Lightsail 스냅샷 또는 S3)

**선행 의존**: Phase 4 완료

**후행 연계**: T5-04

**리뷰 목표**
- [ ] 백업 파일 일 1회 생성 확인
- [ ] 7일치 보관 후 오래된 파일 자동 삭제 확인
- [ ] 백업 파일로 DB 복원 성공 확인

---

### T5-02 — 서버 모니터링 알림

**의도**
서버 장애를 운영자가 5분 이내 인지할 수 있도록 알림 채널을 구성한다.

**작업 목표**
- AWS Lightsail 메트릭 알림 설정 (CPU, 메모리, 연결 수)
- `GET /health` 외부 헬스체크 모니터링 설정 (UptimeRobot 또는 Lightsail 알람)
- 이메일 또는 Slack Webhook 알림 연결
- 서비스 다운 감지 → 5분 이내 알림

**선행 의존**: Phase 4 완료

**후행 연계**: T5-03

**리뷰 목표**
- [ ] 서비스 의도적 중단 후 5분 이내 알림 수신
- [ ] 알림 내용에 장애 서비스명, 감지 시각 포함

---

### T5-03 — 부하 테스트

**의도**
설계 목표인 CCU 200 기준 응답시간 300ms 이하를 실제로 검증하고 병목 지점을 사전에 식별한다.

**작업 목표**
- 부하 테스트 도구 선택 및 시나리오 작성 (`k6` 또는 `locust`)
- 주요 API 시나리오: `/auth/steam` → `/player/state` → `/player/clicks` → `/gacha/pull`
- CCU 200 기준 RPS 측정
- 응답시간 P50, P95, P99 측정
- 병목 지점(DB, Redis, Go 서버) 식별 및 튜닝

**선행 의존**: T5-01, T5-02

**후행 연계**: 없음

**리뷰 목표**
- [ ] CCU 200 기준 P95 응답시간 300ms 이하
- [ ] 부하 중 에러율 1% 이하
- [ ] DB 커넥션 풀 한계치 확인 (최대 50개 이내 유지)
- [ ] 메모리 누수 없음 (부하 종료 후 메모리 정상 회복)

---

### T5-04 — 복구 테스트

**의도**
실제 장애 상황에서 RTO(1시간 이내 재기동) 기준이 현실적으로 달성 가능한지 검증한다.

**작업 목표**
- T5-01 백업 파일로 DB 전체 복원 절차 문서화 및 실행
- 복원 후 데이터 일관성 검증 (`players`, `player_states`, `inventories` 레코드)
- 서버 재기동 → `GET /health` 정상 응답까지 소요 시간 측정
- 복원 runbook 문서 작성

**선행 의존**: T5-01

**후행 연계**: 없음 (Phase 5 최종)

**리뷰 목표**
- [ ] 백업 → 복원 → 서버 재기동 전체 1시간 이내 완료
- [ ] 복원 후 데이터 일관성 확인 (`pg_dump` 시점과 레코드 수 일치)
- [ ] 복원 runbook 문서가 운영자 없이도 재현 가능한 수준인지 확인

---

## Task 의존성 요약

```
T1-01 ──► T1-02 ──► T1-03 ──► T1-04 ──► T1-05
                │             │
                └──► T1-06    └──► T1-07 ──► T1-08 ──► T1-09 ──► T1-10
                                                                     │
                                                                   T1-11
                                                                     │
Phase 2: T2-01, T2-02, T2-03 ──► T2-04 ──► T2-05
                                                │
Phase 3: T3-01 ──► T3-02 ──► T3-03
                         └──► T3-04
                                │
Phase 4: T4-01 ──► T4-02 ──► T4-03 ──► T4-05
               └──► T4-04

Phase 5: T5-01 ──► T5-04
         T5-02 ──► T5-03
```

## Task 수 요약

| Phase | Task 수 |
|-------|---------|
| Phase 1 | 11개 |
| Phase 2 | 5개 |
| Phase 3 | 4개 |
| Phase 4 | 5개 |
| Phase 5 | 4개 |
| **합계** | **29개** |
