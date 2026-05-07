# T1-10 Feedback v2

- Reviewed at: `2026-05-04`
- Task: [T1-10.md](/F:/stone_server/Task/T1-10.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: second review of `DELETE /api/v1/auth/logout` after feedback reflection

## Verdict

통과. 지난 리뷰에서 지적한 stale-session logout 경로가 실제 HTTP 요청에서도 동작하는 것을 확인했고, 새 세션 보호 동작도 함께 유지됐습니다.

## Re-check Summary

- 현재 세션 로그아웃 → `204`, 이후 동일 JWT 재사용 → `401`: 통과
  - `DELETE /api/v1/auth/logout` 응답 `204`
  - 동일 JWT로 보호 라우트 재요청 시 `401`
- 구 세션 로그아웃 → `204`, 새 세션 current/refresh 보호: 통과
  - refresh로 새 JWT 발급 후 구 JWT로 `DELETE /api/v1/auth/logout` 요청 시 `204`
  - `session:{old_jti}`만 삭제되고 새 세션 `session:{new_jti}`, `session:current:{player_id}`, `refresh:{player_id}`는 유지
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
  - `STALE_LOGOUT_STATUS=204`
  - `STALE_OLD_SESSION_EXISTS=0`
  - `STALE_NEW_SESSION_EXISTS=1`
  - `STALE_CURRENT_PTR={new_jti}`
  - `STALE_REFRESH_EXISTS=1`
  - `STALE_NEW_SESSION_USE_STATUS=200`
- JWT 없는 요청
  - `MISSING_JWT_STATUS=401`

## Notes

- 이번 2차 리뷰에서는 추가 findings가 없습니다.
