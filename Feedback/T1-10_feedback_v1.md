# T1-10 Feedback v1

- Reviewed at: `2026-05-04`
- Task: [T1-10.md](/F:/stone_server/Task/T1-10.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: first review of `DELETE /api/v1/auth/logout`

## Verdict

보류. 현재 세션 logout과 Lua compare-and-delete 자체는 동작하지만, 태스크에 명시된 “구 세션으로 logout 요청 시 204, 새 세션 current/refresh 보호” 시나리오는 실제 라우팅에서 재현되지 않습니다.

## Findings

1. [P1] 구 세션 logout 분기가 실제 HTTP 경로에서는 도달 불가능합니다.
   - [main.go](/F:/stone_server/cmd/server/main.go:88)에서 `/auth/logout`은 `JWTAuth` 뒤의 보호 라우트에 묶여 있습니다.
   - 그런데 T1-09의 session rotation은 구 세션의 `session:{old_jti}`를 이미 삭제하므로, 구 JWT는 [jwt.go](/F:/stone_server/internal/middleware/jwt.go:45) 단계에서 `401`로 차단되고 [session.go](/F:/stone_server/internal/auth/session.go:118)의 stale-session Lua 분기까지 도달하지 못합니다.
   - 실검증에서도 refresh로 새 JWT 발급 후 구 JWT로 `DELETE /api/v1/auth/logout` 요청 시 기대한 `204`가 아니라 `401`이 반환됐습니다.

## Re-check Summary

- 현재 세션 로그아웃 → `204`, 이후 동일 JWT 재사용 → `401`: 통과
  - `DELETE /api/v1/auth/logout` 응답 `204`
  - 동일 JWT로 보호 라우트 재요청 시 `401`
- 구 세션 로그아웃 → `204`, 새 세션 current/refresh 보호: 실패
  - refresh로 새 JWT 발급 후 구 JWT로 `DELETE /api/v1/auth/logout` 요청 시 `401`
  - 새 세션 자체는 보호됨
    - `session:{new_jti}` 유지
    - `session:current:{player_id}` 유지
    - `refresh:{player_id}` 유지
    - 새 JWT 보호 라우트 요청 `200`
- Lua 스크립트 원자성 단위 테스트: 통과
  - `go test ./...` 통과
- JWT 없는 로그아웃 요청 → `401`: 통과

## Verification

- `go test ./...` 통과
- `docker compose up -d --build` 통과
- 현재 세션 logout 실검증
  - `CURRENT_LOGOUT_STATUS=204`
  - `CURRENT_REUSE_STATUS=401`
  - `CURRENT_SESSION_EXISTS=0`
  - `CURRENT_PTR_EXISTS=0`
  - `CURRENT_REFRESH_EXISTS=0`
- 구 세션 logout 실검증
  - `STALE_LOGOUT_STATUS=401`
  - `STALE_NEW_SESSION_EXISTS=1`
  - `STALE_CURRENT_PTR={new_jti}`
  - `STALE_REFRESH_EXISTS=1`
  - `STALE_NEW_SESSION_USE_STATUS=200`
- JWT 없는 요청
  - `MISSING_JWT_STATUS=401`

## Notes

- [dashboard.html](/F:/stone_server/dashboard.html:232) 기본 상태는 현재 `T1-10: 리뷰 중`으로 되어 있어 추가 수정은 하지 않았습니다.
