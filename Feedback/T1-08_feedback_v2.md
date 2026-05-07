# T1-08 Feedback v2

- Reviewed at: `2026-05-04`
- Task: [T1-08.md](/F:/stone_server/Task/T1-08.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: second review of JWT middleware after feedback reflection

## Verdict

통과. 지난 리뷰에서 지적한 보호 라우트의 rate limit 식별자 문제가 재현되지 않았고, JWT 미들웨어 체크리스트도 모두 충족했습니다.

## Re-check Summary

- 보호 라우트 rate limit 식별자: 개선 확인
  - 유효 JWT로 `POST /api/v1/gacha/pull` 호출 시 Redis 키가 `ratelimit:{player_id}:POST:/api/v1/gacha/pull` 형태로 생성됨
  - 공개 라우트 `POST /api/v1/auth/steam`은 계속 IP 기준 키 `ratelimit:172.19.0.1:POST:/api/v1/auth/steam` 사용
- 유효 JWT + Redis `session:{jti}` 존재: 통과
  - 보호 라우트 요청 `200`
- 만료된 JWT: 통과
  - 임의 만료 JWT로 보호 라우트 요청 시 `401`
  - 응답 본문: `{"code":"UNAUTHORIZED","error":"invalid or expired token"}`
- Redis에서 `jti` 수동 삭제 후 유효 JWT 요청: 통과
  - `session:{jti}` 삭제 후 동일 JWT 재사용 시 `401`
- `Authorization` 헤더 없는 요청: 통과
  - 보호 라우트 헤더 없이 호출 시 `401`

## Verification

- `go build ./...` 통과
- `docker compose up -d --build` 통과
- 실검증 결과
  - `AUTH_PLAYER_ID=3f9ab747-cb68-4592-b4de-d1d37f923207`
  - `PROTECTED_STATUS=200`
  - `RATE_KEYS=ratelimit:3f9ab747-cb68-4592-b4de-d1d37f923207:POST:/api/v1/gacha/pull ratelimit:172.19.0.1:POST:/api/v1/auth/steam`
  - `REVOKED_STATUS=401`
  - `MISSING_STATUS=401`
  - `EXPIRED_STATUS=401`

## Notes

- 이번 2차 리뷰에서는 추가 findings가 없습니다.
