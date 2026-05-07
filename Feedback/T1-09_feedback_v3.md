# T1-09 Feedback v3

- Reviewed at: `2026-05-04`
- Task: [T1-09.md](/F:/stone_server/Task/T1-09.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: third review of `POST /api/v1/auth/refresh` after feedback reflection

## Verdict

통과. 지난 리뷰에서 지적한 동시 refresh 경쟁 조건이 재현되지 않았고, refresh 체크리스트도 모두 충족했습니다.

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
- 동시 refresh 경쟁 조건: 개선 확인
  - 같은 `refresh_token` 동시 5요청 결과 `REFRESH_RESPONSES=200,409,409,409,409`
  - 성공 응답 JWT 사용 결과 `SUCCESSFUL_JWT_USE=200`

## Verification

- `go test ./...` 통과
- `docker compose up -d --build` 통과
- 단일 refresh 검증
  - `SESSION_OLD_EXISTS=0`
  - `SESSION_NEW_EXISTS=1`
  - `SESSION_CURRENT={new_jti}` 갱신 확인
  - `LOCK_EXISTS_AFTER=0`
  - `COOLDOWN_EXISTS_AFTER=1`

## Notes

- 이번 3차 리뷰에서는 추가 findings가 없습니다.
- [dashboard.html](/F:/stone_server/dashboard.html:232) 기본 상태는 이미 `T1-09: 완료`로 반영되어 있습니다.
