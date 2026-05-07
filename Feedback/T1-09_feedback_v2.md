# T1-09 Feedback v2

- Reviewed at: `2026-05-04`
- Task: [T1-09.md](/F:/stone_server/Task/T1-09.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: second review of `POST /api/v1/auth/refresh` after feedback reflection

## Verdict

보류. per-player lock이 추가되면서 동시 refresh 경쟁은 줄었지만, 같은 `refresh_token` 동시 요청 묶음에서 여전히 `200`이 2건 발생했고 그중 한 JWT는 첫 사용 시점에 이미 `401`로 무효였습니다.

## Findings

1. [P1] 동시 refresh에서 여전히 이미 폐기된 JWT가 성공 응답으로 반환될 수 있습니다.
   - [refresh.go](/F:/stone_server/internal/auth/refresh.go:45)에서 per-player lock을 잡고 한 요청씩 직렬화하지만, refresh 성공 후에도 같은 `refresh_token`은 그대로 유지됩니다.
   - 그래서 첫 요청이 락을 해제한 직후 같은 동시 요청 묶음의 다음 요청이 다시 성공할 수 있고, 그 요청이 직전에 발급한 JWT를 곧바로 폐기합니다.
   - 실검증에서 동시 5요청 결과가 `REFRESH_RESPONSES=200,200,409,409,409`였고, 두 개의 성공 응답 JWT를 즉시 사용해보면 `SUCCESSFUL_JWT_USE=200,401`로 확인됐습니다.
   - 즉 이전보다는 줄었지만, 여전히 서버가 `200`으로 응답한 JWT 중 일부가 사용 전에 이미 죽어 있을 수 있습니다.

## Re-check Summary

- 정상 refresh_token → 새 JWT 발급: 통과
  - `POST /api/v1/auth/refresh` 응답 `200`
- refresh 후 이전 JWT로 요청 → `401` 즉시 반환: 통과
  - 기존 JWT로 보호 라우트 요청 시 `401`
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
- 단일 refresh 검증
  - `SESSION_OLD_EXISTS=0`
  - `SESSION_NEW_EXISTS=1`
  - `SESSION_CURRENT={new_jti}` 갱신 확인
  - `LOCK_EXISTS_AFTER=0`
- 동시 refresh 재검증
  - `REFRESH_RESPONSES=200,200,409,409,409`
  - `SUCCESSFUL_JWT_USE=200,401`

## Notes

- [dashboard.html](/F:/stone_server/dashboard.html:232) 기본 상태는 현재 `T1-09: 리뷰 중`으로 유지되어 있어 추가 수정은 하지 않았습니다.
