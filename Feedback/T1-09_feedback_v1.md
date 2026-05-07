# T1-09 Feedback v1

- Reviewed at: `2026-05-04`
- Task: [T1-09.md](/F:/stone_server/Task/T1-09.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: first review of `POST /api/v1/auth/refresh`

## Verdict

보류. 단일 refresh 흐름과 Lua 세션 교체 자체는 동작하지만, 같은 `refresh_token`으로 동시 요청이 들어오면 여러 `200` 응답이 모두 새 JWT를 발급하고 그중 대부분은 첫 사용 시점부터 이미 무효가 되는 경쟁 조건이 남아 있습니다.

## Findings

1. [P1] 동시 refresh 요청이 즉시 무효인 JWT를 성공 응답으로 발급합니다.
   - [refresh.go](/F:/stone_server/internal/auth/refresh.go:32)에서 `refresh_lookup` 조회와 저장된 `refresh:{player_id}` 검증을 먼저 수행한 뒤, [refresh.go](/F:/stone_server/internal/auth/refresh.go:71)에서 세션 교체만 Lua로 처리합니다.
   - 이 사이에 같은 `refresh_token` 요청이 여러 개 동시에 통과할 수 있어서, 실검증에서 동시 5요청이 모두 `200`을 반환했습니다.
   - 이후 각 응답의 JWT를 바로 보호 라우트에 사용해보면 `401,401,200,401,401`로 확인돼 마지막 요청의 JWT만 살아남았습니다.
   - 즉 서버가 `200`으로 돌려준 JWT가 이미 사용 불가능한 상태일 수 있어, refresh 성공 응답의 의미가 깨집니다.

## Re-check Summary

- 정상 refresh_token → 새 JWT 발급: 통과
  - `POST /api/v1/auth/refresh` 응답 `200`
- refresh 후 이전 JWT로 요청 → `401` 즉시 반환: 통과
  - 기존 JWT로 `POST /api/v1/gacha/pull` 요청 시 `401`
- refresh 후 새 JWT로 요청 → `200` 정상: 통과
  - 새 JWT로 동일 보호 라우트 요청 시 `200`
- 무효 refresh_token → `401 INVALID_REFRESH_TOKEN`: 통과
  - 임의 토큰 `not-a-real-token` 요청 시 `401`
  - 응답 본문: `{"code":"INVALID_REFRESH_TOKEN","error":"invalid or expired refresh token"}`
- Lua 스크립트 원자성 단위 테스트: 통과
  - `go test ./...` 통과

## Verification

- `go test ./...` 통과
- `docker compose up -d --build` 통과
- Redis 세션 교체 검증
  - `session:{old_jti}` 삭제 확인
  - `session:{new_jti}` 생성 확인
  - `session:current:{player_id}`가 `new_jti`로 갱신된 것 확인
- 동시 refresh 실검증
  - `REFRESH_RESPONSES=200,200,200,200,200`
  - `SUCCESSFUL_JWT_USE=401,401,200,401,401`

## Notes

- [dashboard.html](/F:/stone_server/dashboard.html:232) 기본 상태는 현재 `T1-09: 리뷰 중`으로 되어 있어 추가 수정은 하지 않았습니다.
