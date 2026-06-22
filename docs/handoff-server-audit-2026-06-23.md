# 핸드오프 — 서버 audit 수정 (2026-06-23)

> 이 문서는 `docs/plans/audit-fix-plan-2026-06-20.md`(27개 결정지점)의 **서버 lane** 실행을 이어받기 위한 인계 문서다. 컨텍스트 전환에도 작업을 이어가도록 현재 상태·남은 일·방법·주의사항을 담는다.

## 0. 한눈에

- **기반 계획**: [`docs/plans/audit-fix-plan-2026-06-20.md`](plans/audit-fix-plan-2026-06-20.md) — §0 표에 항목별 ✅/◐/⬜/⏸️ 상태 표기됨(이게 single source of truth).
- **진행 플로우**: **claude 구현 → codex 검증 → 수정 → 커밋** (메모리 [project_workflow_claude_codex]).
- **머지됨**: [PR #27](https://github.com/snoowood/stone_server/pull/27)(`0552c46`, A0 보안+A2 전체+clean A3, 7커밋) · [PR #28](https://github.com/snoowood/stone_server/pull/28)(P3-SERVER + 본 핸드오프). **현재 main = #27 + #28.**
- **🔜 다음 세션 시작점(내일)**: ① §3 **LOG-5**(즉시 가능·결정 불필요, 단 `balance_before` dialect 주의) → ② §8 **503 일관성**(소공수). 그 뒤 결정필요(§3 하단)·인프라(§6)·클라(§7)·R1 dialect(§5).
- **범위 원칙**(메모리 [project_role_split]): 🟢 서버 단독 진행 / 🟦 인프라(compose·script)는 별도 배치 / 🔵 클라(Unity, 다른 담당자)는 영향·이유 공유 후 승인.

## 1. 검증 플로우 (그대로 이어쓸 것)

```
codex exec -s read-only - < /tmp/prompt.txt > /tmp/out.txt 2>&1
```
- 미커밋 diff 를 read-only 로 검토시킨다. 프롬프트에 변경 요약 + 검증 관점 + "문제없으면 '이상 없음' 결론"을 명시.
- ⚠️ `codex exec review --uncommitted` 는 커스텀 프롬프트와 **동시에 못 쓴다**(arg 충돌). 위 일반 exec 방식 사용.
- ⚠️ codex 는 read-only 샌드박스라 `go test` 실행은 못 한다(정적 리뷰). **빌드/테스트는 이쪽에서 직접 돌린다**: `go build ./...`, `go vet ./...`, `go test ./...`.
- **codex 가 라운드마다 실결함을 잘 잡는다** — 특히 라이브러리 동작은 추측 말고 **실제 소스 직접 확인**이 정답(예: gin v1.12.0 `recovery.go` 의 broken-pipe 판정은 `errors.Is(EPIPE/ECONNRESET/http.ErrAbortHandler)`). 발견 → 수정 → 재검증으로 "이상 없음"까지 닫는다.

## 2. 완료 (제외)

### PR #27 (머지됨)
R2(코드절반) · P0-4 · E2E-3 · LOG-2 · LOG-3 · LOG-4① · SRE-4 · SRE-5 · LOG-7 · SEC-3 · SEC-4(TrustedProxies 가드) · XC-3. 전 패키지 build/vet/test 통과 + codex 검증.

### PR #28 (머지됨)
- **P3-SERVER**: SEC-6 **1단계**(NewToken 에 `aud` 발급, **검증 강제는 2단계** — 아래 §3) + SRE-8 주석 정정 + .env APP_ENV 경고 + `non-production`→`development` 주석(steam/achievement/devauth/steam_mock/stub).
- 본 핸드오프 문서.

## 3. 남은 서버 lane 작업 (🟢, 인프라/클라 제외)

> 우선순위순. 각 항목의 **블로커/결정**을 반드시 확인할 것.

### 즉시 가능 (결정 불필요)
| ID | 내용 | 위치 | 메모 |
|---|---|---|---|
| **LOG-5** ← **내일 시작** | 경제 감사 `balance_before`/`after`/`accrued_pts` 완전 기록 | gacha.go/vow.go INSERT + 마이그레이션 000016 + sqlitedb 스키마 | ⚠️ `balance_before` 는 `UPDATE ... RETURNING` 이 **새 값만** 줘서 old 값을 못 읽음. **권장 접근(SQLite 활성경로)**: 같은 tx 안에서 accrual UPDATE **직전** `SELECT enlightenment_pts`(SQLite 는 MaxOpenConns=1 직렬화라 정확) → `accrued = after - before + cost`. PG(R1 후)는 `FOR UPDATE` 로 잠금 필요. balance_after 는 이미 RETURNING 으로 있음 |

### SEC-6 2단계 (24h 후 별도 배포) ⚠️
- 1단계(aud 발급)는 브랜치에 있음. **24h(=jwtExpiry) 경과로 기존 aud 없는 토큰이 전부 회전된 뒤**, `internal/middleware/jwt.go` 의 `ParseWithClaims` 2곳(JWTAuth/JWTSignatureAuth)에 `jwt.WithIssuer("stone-server")` + `jwt.WithAudience("stone-server")` 옵션 추가. 1단계보다 먼저 하면 기존 토큰이 401 된다(절대 금지).

### 결정 필요 (단독 진행 금지)
| ID | 결정 사항 | 누가 |
|---|---|---|
| **LOG-4②** | 진단 엔드포인트 `/internal/diag/economy` 의 **인증 모델** — 현재 admin-auth 없음. 옵션: 정적 토큰(env), 내부망 IP 제한, 또는 `/internal` dev 전용 유지 | 서버 owner |
| **SEC-4 per-IP** | mutation per-IP 보조 카운터의 **한도 값** — 부하 사이징 필요(NAT 다중 플레이어 안 깨지게). 계획서도 출시 후 텔레메트리로 미룸 | owner/부하테스트 |
| **R1 (B) dialect** | **XL.** prod PostgreSQL 실지원. 아래 §5 참조 | owner (팀결정은 prod=PG+Redis로 確定) |

## 4. 트랙 C — 서버 부분 (🟢🔵, 기획/design + 클라 묶음)
- **ECON-3** (서버): Steam auth 시 `last_sync_at = now` 미정산 리셋 → 오프라인(앱 닫힘) gap 미적립. 클라 R3b resume 리셋과 한 동작. 서버 단독 적용은 가능(클라가 멱등 처리)하나 **경제 design 결정** 필요. 위치: `internal/auth/handler.go`(E2E-3 변경과 같은 곳).
- **SYNC-3** (서버): `updateLoginStreak` 에서 `enlightenment_rate` 를 streak 기반 갱신 + `/state` 전파. **기획 결정**(클릭 스트릭보너스 포기, rate 단일소스 위치).

## 5. R1 (B) dialect — 최대 잔여 작업 (XL)

[팀결정 06-20] **dev=SQLite / prod=PostgreSQL+Redis**. 현 코드는 **100% SQLite 방언**이고 pgx 어댑터는 무변환 전달이라, prod PG 가 동작하려면 `pkg/store` 에 **dialect 레이어**가 필수:
- `?` → `$N` rebind
- `strftime('%s',x)` → `EXTRACT(EPOCH FROM x)` / `strftime('%Y-..','now')` → `now()`·`to_char` / `date('now')` → `CURRENT_DATE` / `date('now','-1 day')` → `CURRENT_DATE-1`
- `steam_synced=1` 등 boolean 매핑
- 42곳 SQL 정합 + **양 backend 통합테스트**.
- §5 파급: prod=PG+Redis 확정으로 SYNC-4·DB-2/3(잔고 타입 NUMERIC vs REAL)·SRE-3(마이그레이션 dirty)·DB-6(파괴적 down)·LOG-8 이 다시 스코프로.
- **선행**: 이 작업 전 prod 를 PG 로 올리면 `/health` 가 `db.Ping` 만 해서 부팅은 성공하고 첫 트래픽에 전 DB경로 500. (R1 stopgap = 그때까지 PG 모드 fail-fast + prod 임시 SQLite — 🟦 인프라 배치 소속)

## 6. 인프라 배치 (🟦, 본 작업에서 제외 — 별도)
R1 stopgap · P0-3(compose `SQLITE_PATH:""` 가드 + deploy preflight) · R2 인프라절반(compose APP_ENV 필수화 `:?` + preflight) · SRE-6(compose wget healthcheck). **인터림 프로덕션 DB 방향 결정**(prod=SQLite 임시 vs PG경로)에 의존. R2 인프라절반은 배포계약 변경(시크릿에 APP_ENV 필수).

## 7. 클라 (🔵, 본 작업에서 제외 — 담당자 승인)
R3a · R3b · R4 · R5 · E2E-2(클라) · XC-6 · P3-CLIENT(NET-5). **서버 단독 가능 1건**: R4 죽은코드 3종 제거(`ErrorPresenter.cs`, 서버 emit 0건 확인).

## 8. codex PR 리뷰가 남긴 후속거리 (audit 외)
- **503 계약 일관성**: SRE-5 가 JWT 경로만 503+Retry-After. 로그인(`internal/auth/handler.go` AuthSteam 의 KV 실패)·리프레시(`internal/auth/refresh.go`)는 같은 KV 장애에 여전히 500. SRE-5 "인증 전구간" 의도 완성하려면 확장. 서버 단독·소공수.

## 9. 주의사항 (gotchas)

- **이중 스토어**: dev=SQLite(+MemStore), prod=PG(+Redis)는 `SQLITE_PATH` 환경변수로 분기(`cmd/server/main.go:100`). 현재 활성 경로는 dev(SQLite). 새 SQL 은 **기존 SQLite 방언과 일치**시킬 것(R1(B) 가 일괄 변환 예정).
- **sqlitedb 미러**: `pkg/sqlitedb/sqlitedb.go` 가 PG 마이그레이션을 reused-DB 용으로 미러(legacy cleanup ALTER 패턴). 새 컬럼 추가 시 PG 마이그레이션 + sqlitedb 스키마 const + reused-DB ALTER(중복 무시) 셋 다 챙길 것. (E2E-3 에서 0009 미러 메움 — 그 이후 미러 정합 점검 여지 있음)
- **Docker SQLITE_PATH 함정**(메모리 [project_docker_sqlite_path_gotcha]): `.env` 의 SQLITE_PATH 가 컨테이너로 새면 앱 크래시. compose 에 `SQLITE_PATH:""` 무력화(P0-3).
- **APP_ENV**: 이제 화이트리스트 `{development, production}` fail-fast(빈값/오타 부팅 거부). dev 게이트는 `== "development"`. (R2)
- **마이그레이션 번호**: 다음은 **000016**(000015=E2E-3 백필). 신규는 PG-valid 로 작성, sqlitedb 는 별도(migrations/*.sql 은 PG-only, golang-migrate).
- **로그 레벨 컨벤션**: 5xx=Error / 4xx=Warn(request 라인, LOG-7). 정상 거절=Debug, 경합/desync=Info, 인프라 실패=Error. reject_code 는 `c.Set("reject_code", ...)` → RequestLogger 가 읽음.
- **gin 미들웨어 순서**: `RequestLogger()`(request_id 바운드 로거 심음) → `Recovery()`(그 로거 사용) 순서 필수.

## 10. 다음 권장 순서 (서버 단독)
1. LOG-5 (감사 완전기록) — dialect 주의
2. 503 일관성(§8) — 소공수
3. [결정 후] LOG-4② 진단 엔드포인트 / SEC-4 per-IP / ECON-3 / SYNC-3
4. SEC-6 2단계 (배포 24h 후)
5. [별도] 인프라 배치 → R1(B) dialect (XL)
