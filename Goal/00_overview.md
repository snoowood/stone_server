# Stone Server — 전체 개요
> v1.5 — 5차 피드백 반영 (2026-05-03) — 최종 확정

## 목표

stone_project(Unity 데스크탑 위젯 게임)의 라이브 서비스를 위한 백엔드 서버 구축.  
현재 완전 오프라인인 클라이언트에 서버 검증 레이어를 추가하여 핵/어뷰징 방지 및 클라우드 저장을 구현한다.

## 구현 대상 시스템 (5개)

| # | 시스템 | 목적 |
|---|--------|------|
| 1 | Steam 서버 인증 | 플레이어 신원 검증, JWT 발급 |
| 2 | 가챠 시스템 | 서버 사이드 RNG, 쿨다운 강제 |
| 3 | 시간 어뷰징 검증 | 클라이언트 시간 조작 차단 |
| 4 | 인벤토리 동기화 | 서버 권위(Server-Authoritative) 저장 |
| 5 | Steam 업적 시스템 | 서버에서 조건 검증 후 업적 unlock |

## 제약 조건

- 월 서비스 비용 **₩50,000 이하**
- 백엔드 언어: **Go**
- 인프라: **AWS Lightsail $20 플랜 (서울 리전)**
  - 스펙: 4GB RAM / 2 vCPU / 80GB SSD / 4TB 전송
  - 월 비용: ~₩27,400

## 기술 스택

| 레이어 | 기술 | 선택 이유 |
|--------|------|-----------|
| 언어 | Go 1.22+ | 낮은 메모리, 높은 처리량, 단일 바이너리 |
| 웹 프레임워크 | Gin | 경량, Go 생태계 표준 |
| DB | PostgreSQL 15 | 트랜잭션 안정성, 가챠 이력 보장 |
| 캐시 | Redis 7 | 세션(jti), 쿨다운, rate limit |
| 인증 | JWT (RS256) + Redis 하이브리드 | 아래 "인증 모델" 항목 참조 |
| 배포 | Docker Compose | 단일 서버 올인원 운영 |
| 리버스 프록시 | Nginx | SSL 종료, 443 → 8080 |
| SSL | Let's Encrypt (Certbot) | 무료 |

## 인증 모델 (확정)

**하이브리드 세션** 방식을 채택한다.

- JWT는 RS256으로 서명하되, **jti(JWT ID)를 Redis에 저장**
- 모든 요청에서 JWT 서명 검증 + Redis jti 존재 여부 동시 확인
- 로그아웃 또는 강제 무효화 시 Redis에서 jti 삭제 → 즉시 차단 가능
- 순수 Stateless JWT는 로그아웃 후에도 만료 전까지 유효한 문제가 있어 채택하지 않음

```
Redis 키: session:{jti}  TTL: 24h
```

### 다중 기기 정책 (확정)

**한 플레이어당 활성 세션 1개**를 원칙으로 한다.

- 데스크탑 위젯 특성상 다중 기기 동시 실행 시나리오는 지원하지 않음
- 새 기기에서 로그인 시 기존 JWT의 jti를 Redis에서 삭제 → 이전 세션 즉시 무효화
- Refresh Token도 플레이어 단위 1개 유지 (`refresh:{player_id}`에 최신 토큰만 저장)
- 다중 기기 지원이 필요해지면 `session:{player_id}` 보조 키로 이전 jti 추적 후 일괄 삭제하는 방식으로 확장

## 서버 권위 원칙

**모든 상태 변경은 서버가 검증 가능한 도메인 이벤트를 통해서만 발생한다.**

- 클라이언트는 상태 변경 요청(이벤트)만 보내고, 서버가 계산 후 반환
- 클라이언트가 delta 값을 직접 보내 서버가 그대로 반영하는 구조는 사용하지 않음
- 예: 클릭 포인트 → `POST /player/clicks { count: N }` → 서버가 `N × pointsPerClick` 계산

## 인프라 구성도

```
[Steam 클라이언트]
      │ GetAuthTicketForWebApi()
      ▼
[Unity Client — stone_project]
      │ HTTPS REST API (JWT)
      ▼
[AWS Lightsail — ap-northeast-2 (서울)]
 ┌─────────────────────────────────────┐
 │  Nginx :443  →  Go App :8080        │
 │                                     │
 │  PostgreSQL :5432   Redis :6379     │
 │  (Docker Compose, 단일 호스트)       │
 └─────────────────────────────────────┘
      │
      ▼ (Steam 업적/인증 검증 시)
[Steamworks Web API]
```

## 단일 호스트 리스크 및 운영 방침

현재 구조는 DB·Redis·앱이 모두 한 호스트에 있어 **호스트 장애 시 전체 서비스 중단**된다.  
이 리스크를 의도적으로 수용하는 이유는 초기 비용 제약(₩50,000/월)이며, 다음 조건을 만족하는 운영 방침을 채택한다.

| 항목 | 기준 |
|------|------|
| DB 백업 | 일 1회 PostgreSQL dump, 7일치 보관 |
| 복구 목표 시간 (RTO) | 장애 인지 후 1시간 이내 재기동 목표 |
| 복구 목표 시점 (RPO) | 최대 24시간 데이터 손실 허용 |
| 업그레이드 조건 | CCU 300 초과 또는 장애 발생 시 분리 아키텍처 검토 |

## 예상 처리 용량 (초기 추정치)

> 아래 수치는 단일 서버 + Go 동시성 모델 기반의 **가정 기반 목표치**이며, 실제 부하 테스트 전까지 보장값이 아니다.

| 지표 | 추정치 |
|------|--------|
| 목표 CCU | 최대 500명 |
| 초당 요청 (RPS) | 최대 200 RPS |
| DB 커넥션 풀 | 최대 50개 |
| Go App 메모리 | ~100MB |
| PostgreSQL 메모리 | ~500MB |
| Redis 메모리 | ~100MB |
| 총 RAM 사용 | ~800MB / 4GB |

## 디렉토리 구조 (예정)

```
stone_server/
├── cmd/server/main.go          # 진입점
├── internal/
│   ├── auth/                   # Steam 인증 + JWT
│   ├── gacha/                  # 가챠 RNG + 쿨다운
│   ├── player/                 # 플레이어 상태, 클릭 이벤트
│   ├── timeguard/              # 시간 어뷰징 검증
│   └── achievement/            # Steam 업적
├── pkg/
│   ├── db/                     # PostgreSQL 연결/마이그레이션
│   └── cache/                  # Redis 클라이언트
├── migrations/                 # SQL 마이그레이션 파일
├── docker-compose.yml
├── Dockerfile
└── .env.example
```
