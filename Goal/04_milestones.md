# Stone Server — 개발 마일스톤
> v1.5 — 5차 피드백 반영 (2026-05-03) — 최종 확정

## 개발 원칙

- 각 Phase는 독립적으로 배포 가능한 상태로 완료
- Phase 완료 기준: 자동 테스트 통과 + 해당 시스템 Unity 연동 확인
- 이전 Phase 완료 전 다음 Phase 진입 금지

## 작업 트랙 구분

이 마일스톤은 두 개의 독립 저장소에 걸쳐 있다.

| 트랙 | 저장소 | 담당 |
|------|--------|------|
| **Server** | `stone_server` | Go 서버 구현 |
| **Client** | `stone_project` | Unity C# 클라이언트 수정 |

각 Phase의 완료 기준은 **두 트랙 모두** 해당 Phase 작업이 끝나야 충족된다.

---

## Phase 1 — 인프라 + 인증 기반

**목표:** 서버가 실제로 뜨고, Steam 로그인이 작동하며, 기본 운영 도구가 갖춰진 상태

### Server 작업
| 작업 | 설명 |
|------|------|
| AWS Lightsail 인스턴스 세팅 | 서울 리전 $20 플랜, SSH 접속 확인 |
| Docker Compose 구성 | Go App + PostgreSQL + Redis + Nginx |
| Let's Encrypt SSL 발급 | 도메인 연결 + HTTPS 확인 |
| Go 프로젝트 초기화 | 모듈, 디렉토리 구조, Dockerfile |
| DB 마이그레이션 연결 | golang-migrate, 001~002 마이그레이션 적용 |
| `GET /health` 구현 | DB·Redis 상태 포함 |
| 구조화 로깅 적용 | zerolog, 요청별 player_id 포함 |
| 환경변수 관리 | `.env` 파일, 민감값 코드 미노출 |
| `POST /auth/steam` 구현 | `GetAuthTicketForWebApi` + Steamworks API 검증 + JWT 발급 |
| `POST /auth/refresh` 구현 | Refresh Token 처리 |
| JWT 미들웨어 구현 | jti Redis 검증 포함 |
| Rate Limiting 미들웨어 | Redis 기반 |

### Client 작업
| 작업 | 설명 |
|------|------|
| Steam SDK 초기화 확인 | Steamworks.NET 연동 |
| `GetAuthTicketForWebApi` 호출 | identity: "stone-server" |
| JWT 저장 및 갱신 로직 | PlayerPrefs 또는 메모리 캐시 |

### 완료 기준
- [ ] `GET /health` 정상 응답 확인
- [ ] Unity 클라이언트에서 Steam 로그인 → JWT 수신 성공
- [ ] 동일 티켓 재사용 → 409 반환 확인
- [ ] **세션 경합 시나리오**: 새 기기 로그인 직후 기존 세션으로 로그아웃 요청 → 새 세션 current/refresh 보호 확인
- [ ] **세션 경합 시나리오**: 로그아웃 후 동일 JWT 재사용 → 401 반환 확인
- [ ] `POST /auth/refresh` 후 이전 JWT로 요청 → 401 반환 확인 (jti 전환 검증)
- [ ] `POST /auth/refresh` 후 새 JWT로 요청 → 200 정상 응답 확인
- [ ] 자동 테스트: 인증 플로우 + compare-and-delete 로그아웃 + refresh jti 전환 단위 테스트 통과

---

## Phase 2 — 시간 검증 + 플레이어 상태 동기화

**목표:** 게임 상태를 서버에서 관리하고, 시간 어뷰징 차단

### Server 작업
| 작업 | 설명 |
|------|------|
| `POST /time/sync` 구현 | drift 계산, warning 응답 |
| `GET /player/state` 구현 | 로그인 시 서버 상태 전체 조회 |
| `POST /player/clicks` 구현 | 클릭 배치 포인트 계산 |
| **클릭 어뷰징 방어 구현** | batch_id 중복 방지, count 상한(300), 시간당 상한(3,000), 연속 요청 간격 검증 |
| 마이그레이션 003~004 적용 | inventories, gacha_logs 테이블 |

### Client 작업
| 작업 | 설명 |
|------|------|
| `SaveManager.cs` 수정 | 로컬 JSON → 로그인 시 서버 GET, 로컬 임시 캐시만 유지 |
| 클릭 배치 전송 | 30초마다 로컬 클릭 수 누적 → `POST /player/clicks` (batch_id UUID 포함) |

### 완료 기준
- [ ] 클라이언트 시간 5분 이상 차이 → `warning: true` 수신 확인
- [ ] 게임 재시작 후 서버 상태 로드 확인 (로컬 JSON 의존 제거)
- [ ] 동일 batch_id 재전송 (5분 이내) → `409 DUPLICATE_BATCH` 확인
- [ ] 동일 batch_id 재전송 (5분 이후) → 새 배치로 처리됨 확인 (**의도적 허용 범위**)
- [ ] count > 300 요청 → `400 INVALID_COUNT` 확인
- [ ] 자동 테스트: 상태 조회·클릭 포인트 계산·어뷰징 방어 단위 테스트 통과

---

## Phase 3 — 가챠 시스템 서버화

**목표:** 가챠 결과를 서버에서 결정, 쿨다운 강제 적용

### Server 작업
| 작업 | 설명 |
|------|------|
| `POST /gacha/pull` 구현 | 서버 RNG, 쿨다운 검증, 트랜잭션, 중복 환급 |
| `GET /gacha/status` 구현 | 쿨다운 상태 조회 |
| `GET /gacha/logs` 구현 | 이력 페이징 |

### Client 작업
| 작업 | 설명 |
|------|------|
| `SariraSystem.cs` 수정 | 로컬 RNG 제거 → `POST /gacha/pull` 호출 |
| 중복 아이템 환급 UI | `is_duplicate: true` 시 포인트 환급 표시 |

### 완료 기준
- [ ] 쿨다운 중 가챠 요청 → `429` + `next_gacha_at` 수신 확인
- [ ] 포인트 부족 요청 → `409 INSUFFICIENT_POINTS` 확인
- [ ] 중복 아이템 획득 → 포인트 환급 처리 확인
- [ ] 장애 시나리오: 트랜잭션 중간 실패 시 롤백 확인
- [ ] 자동 테스트: 가챠 트랜잭션, 쿨다운 로직 단위 테스트 통과

---

## Phase 4 — Steam 업적 시스템

**목표:** 조건 검증 후 서버에서 Steam 업적 unlock

### Server 작업
| 작업 | 설명 |
|------|------|
| 마이그레이션 005~006 적용 | player_achievements, achievement_retry_queue |
| `POST /achievement/unlock` 구현 | 조건 검증 + Steamworks API 호출 |
| `GET /achievement/list` 구현 | 업적 목록 조회 |
| 업적 재시도 백그라운드 워커 | Redis `ach:retry` 큐 소비, 1분 주기 |
| 6개 초기 업적 조건 구현 | DB 조건 쿼리 |

### Client 작업
| 작업 | 설명 |
|------|------|
| 클라이언트 `SetAchievement()` 직접 호출 제거 | 서버 API 호출로 대체 |
| 업적 unlock 이벤트 연결 | 조건 달성 시 `POST /achievement/unlock` |

### 완료 기준
- [ ] 조건 미충족 업적 요청 → `400 CONDITION_NOT_MET` 확인
- [ ] 레전더리 획득 → Steam 업적 자동 unlock 확인 (`steam_synced: true`)
- [ ] Steam API 실패 → 로컬 unlock 확정 (`steam_synced: false`), Redis 큐 적재 확인
- [ ] 큐 워커 재처리 후 `steam_synced: true`로 갱신 확인
- [ ] **업적 비동기 정책 검증**: Steam API 실패 시에도 `player_achievements` DB 기록이 먼저 커밋됨을 단위 테스트로 확인
- [ ] 장애 시나리오: Steam API 전체 다운 → 큐 적재 → 복구 후 일괄 처리 확인
- [ ] 자동 테스트: 업적 조건 검증, 저장 순서(DB 先 → Steam API 後), 멱등성 단위 테스트 통과

---

## Phase 5 — 운영 안정화

**목표:** 라이브 배포 전 운영 필수 항목 완비

### Server 작업
| 작업 | 설명 |
|------|------|
| 자동 DB 백업 | 일 1회 PostgreSQL dump, 7일치 보관 |
| 서버 모니터링 알림 | Lightsail 메트릭 + 이메일/Slack 알림 |
| 부하 테스트 | CCU 200 기준 RPS 측정, 병목 확인 |
| 복구 테스트 | 백업에서 DB 복원 절차 검증 |

### 완료 기준
- [ ] 서버 다운 시 5분 내 알림 수신 확인
- [ ] 백업 → 복원 후 데이터 일관성 확인
- [ ] 부하 테스트 통과: CCU 200 기준 응답시간 300ms 이하

---

## 전체 일정 (예상)

```
Week 1-2   Phase 1  인프라 + Steam 인증
Week 3     Phase 2  시간 검증 + 상태 동기화
Week 4     Phase 3  가챠 서버화
Week 5     Phase 4  Steam 업적
Week 6     Phase 5  운영 안정화
Week 7     버퍼     통합 테스트, 미완 작업, Unity-Server 연동 최종 검증
```

> Week 7은 버퍼 주간. 기능 추가가 아닌 통합 테스트 및 미완 작업 처리 전용.

---

## 장애 시나리오 체크리스트 (Phase별 추가 검증)

| 시나리오 | 확인 Phase |
|---------|-----------|
| 가챠 트랜잭션 중간 실패 → 포인트 차감 없이 롤백 | Phase 3 |
| Redis 재시작 → 쿨다운 유실, DB fallback으로 복구 | Phase 3 |
| 클릭 batch_id 중복 전송 → 포인트 이중 지급 없음 | Phase 2 |
| 클릭 batch_id 동일값 5분 이후 재전송 → 새 배치로 처리됨 확인 (**의도적 허용 — 방지 범위 밖**) | Phase 2 |
| Steam API 전체 장애 → 업적 큐 적재 후 복구 | Phase 4 |
| 업적 Steam API 실패 → DB 로컬 unlock은 보존됨 | Phase 4 |
| DB 연결 실패 → `/health` 503, 서비스 graceful 처리 | Phase 1 |
| JWT 만료 중 Refresh → 새 JWT 정상 발급 | Phase 1 |
| 새 기기 로그인 → 이전 세션 즉시 무효화 확인 | Phase 1 |
| 새 로그인 직후 구 세션 로그아웃 → 새 세션 current/refresh 보호 확인 | Phase 1 |
