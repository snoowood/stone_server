# Stone Server

Unity 데스크탑 위젯 게임 **stone_project**의 백엔드 서버.  
Steam 인증, 해탈력(N Power) 패시브 누적 동기화, 가챠, 인벤토리, Steam 업적을 서버 권위(Server-Authoritative) 방식으로 처리한다.

## 기술 스택

| 레이어 | 기술 |
|--------|------|
| 언어 | Go 1.25 |
| 웹 프레임워크 | Gin |
| DB | PostgreSQL 15 (운영) / SQLite (로컬 개발) |
| 캐시 / 세션 | Redis 7 (운영) / In-Memory (SQLite 모드) |
| 인증 | JWT RS256 + Redis jti 하이브리드 |
| 리버스 프록시 | Nginx (SSL 종료, 443 → 8080) |
| 배포 | Docker Compose |

## 디렉토리 구조

```
stone_server/
├── cmd/server/main.go          # 진입점
├── internal/
│   ├── auth/                   # Steam 인증 + JWT 발급/갱신/로그아웃
│   ├── gacha/                  # 가챠 RNG + 쿨다운
│   ├── player/                 # 플레이어 상태 조회 + 해탈력 동기화
│   ├── achievement/            # Steam 업적 + 재시도 워커
│   ├── middleware/             # JWT 인증, Rate Limiter, Request Logger
│   └── timeguard/              # 서버 시간 동기화
├── pkg/
│   ├── db/                     # PostgreSQL 연결 + golang-migrate
│   ├── cache/                  # Redis 클라이언트
│   ├── kvstore/                # KVStore 인터페이스 (Redis / In-Memory)
│   ├── store/                  # DB 인터페이스 (pgx / database/sql)
│   ├── sqlitedb/               # SQLite 인라인 스키마 + 초기화
│   ├── config/                 # 환경변수 로드 및 검증
│   └── logger/                 # zerolog 초기화
├── migrations/                 # PostgreSQL 마이그레이션 SQL
├── scripts/
│   ├── load-test/              # k6 부하 테스트 시나리오
│   └── restore.sh              # DB 백업 복구 스크립트
├── nginx/                      # Nginx 설정 + 개발용 self-signed 인증서
├── Dockerfile
├── docker-compose.yml
└── .env.example
```

## 로컬 실행 (SQLite 모드)

PostgreSQL / Redis 없이 SQLite 파일 하나로 바로 실행할 수 있다.

```powershell
# PowerShell
$env:SQLITE_PATH="./dev.db"
$env:APP_ENV="development"
$env:SERVER_PORT="8080"
$env:JWT_PRIVATE_KEY="<RSA 개인키 PEM>"
go run ./cmd/server
```

JWT 개인키가 없으면 먼저 생성한다.

```bash
openssl genrsa -out private.pem 2048
# PEM 내용을 JWT_PRIVATE_KEY 환경변수에 설정
```

서버가 뜨면 헬스체크로 확인한다.

```bash
curl http://localhost:8080/api/v1/health
```

`APP_ENV=development`이면 Steam mock 클라이언트가 활성화되고 `/api/v1/auth/dev`, `/api/v1/internal/dev-token` 엔드포인트가 노출된다.

## Docker Compose 실행 (PostgreSQL + Redis 모드)

```bash
cp .env.example .env
# .env 파일에서 DB_PASSWORD, JWT_PRIVATE_KEY, STEAM_API_KEY 등 설정

docker compose up -d
```

앱 로그 확인:

```bash
docker compose logs -f app
```

## 환경변수

| 변수 | 필수 | 기본값 | 설명 |
|------|------|--------|------|
| `JWT_PRIVATE_KEY` | ✅ | — | RSA 개인키 PEM |
| `DB_URL` | PG 모드 | — | PostgreSQL 연결 URL |
| `REDIS_URL` | PG 모드 | — | Redis 연결 URL |
| `SQLITE_PATH` | SQLite 모드 | — | SQLite 파일 경로 (설정 시 PG/Redis 불필요) |
| `SERVER_PORT` | ❌ | `8080` | HTTP 리슨 포트 |
| `APP_ENV` | ❌ | `development` | `production`이면 Steam API 필수, Gin release 모드 |
| `STEAM_API_KEY` | production | — | Steam Web API Key |
| `STEAM_APP_ID` | production | — | Steam App ID |
| `TRUSTED_PROXIES` | ❌ | — | 신뢰할 프록시 CIDR (쉼표 구분) |
| `DIAG_INTERVAL_SECS` | ❌ | `0` | DB 풀 + 메모리 진단 로그 주기 (초, 0=비활성) |

## API 엔드포인트

기본 URL: `https://{domain}/api/v1`  
인증이 필요한 엔드포인트는 `Authorization: Bearer {JWT}` 헤더 필수.

### 헬스체크

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/health` | ❌ | 서버·DB·Redis 상태 확인 |

### 인증

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| POST | `/auth/steam` | ❌ | Steam 티켓 검증 + JWT 발급 |
| POST | `/auth/refresh` | ❌ | Refresh Token으로 JWT 재발급 |
| DELETE | `/auth/logout` | 🔒 | 세션 즉시 무효화 |

### 시간 동기화

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| POST | `/time/sync` | ❌ | 서버 시간 + drift 반환 |

### 플레이어

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| GET | `/player/state` | 🔒 | 전체 게임 상태 조회 |
| POST | `/player/sync` | 🔒 | 해탈력 누적 동기화 |

`POST /player/sync`는 서버가 `(now - last_sync_at) × enlightenment_rate`를 계산해 `enlightenment_pts`를 적립하고 `last_sync_at`을 갱신한다.  
클라이언트는 delta를 직접 전송하지 않는다.

### 가챠

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| POST | `/gacha/pull` | 🔒 | 가챠 1회 실행 |
| GET | `/gacha/status` | 🔒 | 쿨다운 상태 확인 |
| GET | `/gacha/logs` | 🔒 | 가챠 이력 조회 |

### 업적

| 메서드 | 경로 | 인증 | 설명 |
|--------|------|------|------|
| POST | `/achievement/unlock` | 🔒 | 업적 unlock 요청 |
| GET | `/achievement/list` | 🔒 | 내 업적 목록 |

## Rate Limit 정책

| 엔드포인트 | 제한 |
|-----------|------|
| `POST /auth/steam` | 10회 / 분 / IP |
| `POST /gacha/pull` | 5회 / 분 / player |
| `POST /player/sync` | 4회 / 분 / player |
| `POST /achievement/unlock` | 20회 / 분 / player |
| 그 외 | 60회 / 분 / player |

## 부하 테스트

k6가 설치된 환경에서 실행한다.

```bash
# smoke 테스트 (소규모)
TARGET_URL=https://localhost ./scripts/load-test/run.sh smoke

# 부하 테스트 (200 VU, 180초 steady)
TARGET_URL=https://localhost ./scripts/load-test/run.sh load
```

서버 메모리 및 DB 풀 모니터링:

```bash
DIAG_INTERVAL_SECS=5 # .env에 추가 후 재시작
docker compose logs -f app | grep '"message":"diag"'
```

## DB 마이그레이션

PostgreSQL 모드에서는 서버 시작 시 `migrations/` 폴더의 SQL을 자동으로 적용한다.  
SQLite 모드에서는 `pkg/sqlitedb/sqlitedb.go`의 인라인 스키마가 사용된다.

## 백업 복구

```bash
# PostgreSQL 덤프로부터 복구
./scripts/restore.sh <dump_file> [db_url]
# 종료 코드: 0=성공, 1=인수오류, 2=복구실패, 3=헬스타임아웃, 4=정합성오류
```
