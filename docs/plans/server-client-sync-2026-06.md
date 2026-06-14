# 서버-클라이언트 동기화 점검 & 인계 (2026-06)

> 작업2 "서버 클라 동기화 필요한 것 파악 → 누락/개선 계획". 서버(stone_server, Go)와
> 클라(stone_project, Unity C#) 양쪽 소스를 대조 검증한 결과와, **클라 담당자에게
> 넘기는 수정 지시**를 담는다.
>
> 역할 분담: 서버는 본 레포 담당자 lane, 클라(`stone_project`)는 별도 담당자 lane.
> 아래 🔴 표시 항목은 **클라 변경이 필요**하므로 영향·이유를 공유하고 승인 후 진행.

## 결론 요약

- **DTO 필드명/타입은 snake_case 변환 기준 전부 일치**, 클라가 호출하는 13개 엔드포인트는
  모두 서버에 존재(**누락 엔드포인트 없음**).
- 불일치는 전부 **에러 코드 문자열 / 429 바디 형태 / enum 케이싱**에 몰려 있다.
- 핵심 수정 3건(M1·G1·쿨다운)은 **모두 클라 `ErrorPresenter`/`ApiClient`/`Gacha` 영역** →
  클라 담당자 한 묶음 작업 권장. **서버 단독으로 해야 할 코드 변경은 없음**(아래 §3).
- 별도 트랙: `/auth/steam` 클라 미구현(T5-01)으로 **Prod/Staging 부팅 불가**, 클라 토큰
  평문 저장(H2)은 보안 트랙.

## 1. 동기화 드리프트 표

| # | 항목 | 서버 | 클라 | 불일치 / 영향 | 담당 |
|---|---|---|---|---|---|
| M1 | rate-limit 에러 코드 | `RATE_LIMIT_EXCEEDED` (`internal/middleware/ratelimit.go:56`) | CodeMap 에 `RATE_EXCEEDED`만 존재 (`Assets/Scripts/Network/UI/ErrorPresenter.cs:36`) | 코드 문자열 불일치 → 미매핑 → 429 시 **일반 "unknown error" 토스트** | 🔴 클라 |
| G1 | `CAIRN_SLOT_NOT_FOUND` (404) | gacha pull 시 emit (`internal/gacha/gacha.go:144`) | CodeMap 에 없음 (grep clean) | 클라 미처리 → 일반 에러 토스트 | 🔴 클라 |
| C1 | `COOLDOWN_ACTIVE` 429 바디 | `{error, code, next_gacha_at}` 커스텀 바디. **단 두 경로로 형태가 다름**: pre-check(`gacha.go:116-121`)는 `next_gacha_at` **항상** 포함, tx-race(`gacha.go:131-135`)는 **best-effort**(값을 알 때만 포함, 동시 pull 경합 시 누락 가능) | 429 를 `ErrorDto{error, code}` 로만 파싱 (`Assets/Scripts/Network/Core/ApiClient.cs:154-161`) | `next_gacha_at` **유실** → 쿨다운 남은시간 UI 불가 (코드 자체는 Silent 매핑됨). 클라는 `next_gacha_at` 를 **선택적**으로 다뤄야 함 | 🔴 클라 (+선택적 서버) |
| V1 | vow `result` 케이싱 | PascalCase `"Success"`/`"Failed"` (`internal/vow/vow.go:81`) | `FusionResponseDto.Result` (string 그대로) (`Assets/Scripts/Network/Dto/FusionDto.cs`) | 다른 enum 은 lowercase, 이것만 Pascal → 클라 비교 로직 정확 일치 필요 | 🟡 클라 검증 |
| T1 | nullable 시간 직렬화 | `*time.Time` → JSON `null` (`next_gacha_at`, `last_sync_at`) | `DateTime?` (Nullable) | 형태 일치 추정, round-trip 회귀 확인 권장 | 🟡 클라 검증 |
| — | `/vow/pray` 경로 | 라우트 `/vow/pray` | `FusionApi` 가 `/vow/pray` 로 POST (`Assets/Scripts/Network/Services/FusionApi.cs:31`) | **불일치 없음**(C# 만 Fusion 리네임, 경로 유지) | — |
| — | `achievement/list` 형태 | 래퍼 객체 `{achievements:[...]}` | `AchievementListResponseDto.Achievements` 래퍼 기대 | **불일치 없음** | — |
| S1 | `/auth/steam` | 구현됨(public, dev 는 mock) | **호출 없음**(주석만, T5-01) | 클라 미구현 → **Prod/Staging 부팅 불가**(`STEAM_AUTH_REQUIRED`) | 🔴 클라(별도 T5) |
| H2 | 토큰 at-rest | (서버 무관) | base64 평문 JSON, no DPAPI (`Assets/Scripts/Network/Auth/TokenStore.cs`) | 보안 갭 | 🔴 클라(보안 트랙) |
| — | `/internal/dev-token` 에러 바디 | `{error}` only, `code` 없음 (`internal/auth/devtoken.go`) | `code=null` fallback 처리 (`ApiClient.cs:155-157`) | dev 한정, 무해(클라가 이미 흡수) | — |

## 2. 클라 수정 지시 (🔴 — 영향·이유 공유 후 승인)

세 건 모두 클라 `ErrorPresenter`/`ApiClient`/`Gacha` 영역이라 **한 PR로 묶기 권장**.

### M1 — `RATE_LIMIT_EXCEEDED` 코드 정렬 (우선순위 high)
- **현상**: 서버는 rate limit 초과 시 `RATE_LIMIT_EXCEEDED` 를 내려보내지만, 클라 CodeMap 엔
  유사명 `RATE_EXCEEDED`(Silent)만 있어 매핑 실패 → 사용자에게 무의미한 일반 토스트.
- **권고 수정(클라)**: `ErrorPresenter.cs` CodeMap 에 `RATE_LIMIT_EXCEEDED` 항목 추가
  (기존 `RATE_EXCEEDED`/`TOO_FREQUENT` 와 동일하게 `Silent` 권장).
- **대안(서버)**: 서버를 `RATE_EXCEEDED` 로 변경. → **비권장**: `03_api.md` 명세 문자열이
  `RATE_LIMIT_EXCEEDED` 이고, 서버 변경은 명세와 어긋난다. 클라 한 줄 추가가 surgical.
- 효과: S (<2h).

### G1 — `CAIRN_SLOT_NOT_FOUND` (404) 처리 (medium)
- **현상**: `/gacha/pull` 이 잘못된/없는 슬롯에 404 + `CAIRN_SLOT_NOT_FOUND` 반환. 클라
  매핑 없음 → 일반 에러 토스트. (범위 밖 슬롯 `INVALID_SLOT`(`gacha.go:106`) 와 다른 코드임에 유의.)
- **권고 수정(클라)**: `ErrorPresenter.cs` CodeMap 에 `CAIRN_SLOT_NOT_FOUND` 추가
  (적절한 사용자 메시지 또는 Silent + 슬롯 상태 갱신 유도).
- 효과: S (<2h).

### C1 — `COOLDOWN_ACTIVE` 429 의 `next_gacha_at` 추출 (medium-high)
- **현상**: 서버는 429 COOLDOWN_ACTIVE 바디에 `next_gacha_at` 를 싣지만, 클라 `ApiClient`
  는 모든 에러를 `ErrorDto{error, code}` 로만 역직렬화해 `next_gacha_at` 를 버린다. 결과적으로
  쿨다운 카운트다운 UI 가 서버 권위값 없이 클라 추정에 의존.
- **주의(Codex 검증)**: `next_gacha_at` 는 **항상 보장되지 않는다**. pre-check 경로는 항상
  포함하지만, 동시 pull 경합으로 트랜잭션 내부 쿨다운 게이트가 거절하는 tx-race 경로
  (`gacha.go:131-135`)는 값을 알 때만 포함한다. 따라서 클라는 **필드 부재를 정상 처리**해야 한다.
- **권고 수정(클라)** 택1:
  - (A) `ApiClient` 에러 처리에 COOLDOWN_ACTIVE 한정 분기를 추가해 바디에서 `next_gacha_at`
    파싱(있으면 사용) → GachaApi 로 전달. 부재 시 (B)로 폴백. (ErrorDto 고정 파싱이라 특수 분기 필요)
  - (B) 쿨다운 감지 후 `GET /gacha/status` 재조회로 `next_gacha_at` 획득(추가 RTT 1회). 부재
    케이스까지 견고하게 처리 → **권장**(서버 변경 없이 일관 동작).
- 효과: M (<1d).

## 3. 서버 lane (이번 작업 내 필수 서버 변경 — 없음 / 선택 1건)

- M1/G1 의 권고안은 모두 클라 변경이며, 서버는 **현행 유지가 정답**(명세 정합).
- **C1 (선택)**: COOLDOWN_ACTIVE 429 바디가 두 경로에서 형태가 달라(`next_gacha_at` 항상 vs
  best-effort) Codex 가 서버 contract 일관성 이슈로 지적. 단 tx-race 경로에서 값이 nil 인 경우
  서버가 없는 값을 만들 수 없으므로 "항상 보장"은 불가 → **클라가 부재를 견고히 처리(C1-B)**
  하는 것이 정답. 서버 측 필수 변경은 아님. (원하면 tx-race 경로 메시지/형태를 pre-check 와
  맞추는 소소한 정리는 가능하나 동작상 이득은 미미.)
- `/internal/dev-token` 의 `code` 누락은 dev 전용 + 클라가 `code=null` 로 이미 흡수 →
  **변경 불필요**(최하 우선순위, 정합성 NIT).
- 따라서 본 작업2 산출물은 **이 인계 문서 자체**이며, 서버 코드 diff 는 없다(C1 선택 항목 제외).

## 4. 클라 검증 항목 (🟡 — 코드 변경은 결과에 따라)

- **V1 vow `result` 케이싱**: 서버는 `"Success"`/`"Failed"`(Pascal). 클라 Fusion 결과 분기가
  정확히 PascalCase 로 비교하는지 확인. 틀리면 성공/실패 연출이 항상 실패측으로 falls through.
  현 가정(클라가 Pascal 비교)대로면 변경 없음. (서버 lowercase 통일은 양쪽 동시 변경이라 리스크 ↑, 비권장)
- **T1 nullable 시간**: `next_gacha_at`/`last_sync_at` 가 JSON `null` 일 때 `DateTime?` 파싱
  정상 동작하는지 스모크 확인.

## 5. 별도 트랙 (작업2 범위 밖, 기록용)

- **S1 `/auth/steam` (T5-01)**: 서버는 준비 완료(public 라우트, dev mock). 클라 Steamworks
  통합 미완 → Prod/Staging 빌드가 `STEAM_AUTH_REQUIRED` 로 부팅 불가. **클라 대형 작업(L)**,
  작업3 Steam 트랙 및 별도 T5 와 연계.
- **H2 토큰 평문 저장**: 클라 `TokenStore` 가 base64 난독화만 적용(주석에 "보안 아님" 명시).
  클라 보안 트랙. refresh TTL 정책과 함께 재검토.

## 6. 인계 체크리스트 (클라 담당자)

- [ ] M1: `ErrorPresenter` CodeMap 에 `RATE_LIMIT_EXCEEDED`(Silent) 추가
- [ ] G1: `ErrorPresenter` CodeMap 에 `CAIRN_SLOT_NOT_FOUND` 추가
- [ ] C1: `ApiClient` COOLDOWN_ACTIVE 429 의 `next_gacha_at` 파싱(A) 또는 status 재조회(B)
- [ ] V1: Fusion `result` PascalCase 비교 확인
- [ ] T1: nullable 시간 파싱 스모크
- [ ] (T5) `/auth/steam` 통합 일정 협의

---

검증 근거(서버, 본 레포): `internal/middleware/ratelimit.go:56`, `internal/gacha/gacha.go:106,117-121,131-135,144`,
`internal/vow/vow.go:81`, `internal/auth/devtoken.go`. 검증 근거(클라, stone_project, 읽기 전용 확인):
`Assets/Scripts/Network/UI/ErrorPresenter.cs:21-57`, `Assets/Scripts/Network/Core/ApiClient.cs:154-170`,
`Assets/Scripts/Network/Services/FusionApi.cs:31`.
