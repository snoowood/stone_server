# T1-08 Feedback v1

- Reviewed at: `2026-05-04`
- Task: [T1-08.md](/F:/stone_server/Task/T1-08.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: first review of JWT middleware

## Verdict

보류. JWT 검증과 Redis `jti` 검증 자체는 동작하지만, 보호 라우트에서 `player_id`를 Gin Context에 넣어도 현재 미들웨어 등록 순서 때문에 rate limit은 여전히 IP 기준으로 먼저 적용됩니다.

## Findings

1. [P1] 보호 라우트가 여전히 `player_id`가 아닌 IP 기준으로 rate limit 됩니다.
   - [main.go](/F:/stone_server/cmd/server/main.go#L73)에서 `RateLimiter`가 전역 미들웨어로 먼저 등록되고, [main.go](/F:/stone_server/cmd/server/main.go#L87)에서 `JWTAuth`가 보호 라우트 그룹에 나중에 등록됩니다.
   - 재검증에서 유효 JWT로 `POST /api/v1/gacha/pull` 호출 시 Redis 키가 `ratelimit:172.19.0.1:POST:/api/v1/gacha/pull`로 생성됐습니다.
   - 즉 [jwt.go](/F:/stone_server/internal/middleware/jwt.go#L62)에서 주입한 `player_id`가 보호 라우트 rate limit 식별자에 반영되지 않습니다.

## Re-check Summary

- 유효 JWT + Redis `session:{jti}` 존재: 통과
  - `POST /api/v1/auth/steam`으로 발급한 JWT를 사용해 `POST /api/v1/gacha/pull` 호출 시 `200`
- 만료된 JWT: 통과
  - 임의로 만료된 RS256 JWT를 만들어 `POST /api/v1/gacha/pull` 호출 시 `401`
  - 응답 본문: `{"code":"UNAUTHORIZED","error":"invalid or expired token"}`
- Redis에서 `jti` 수동 삭제 후 유효 JWT 요청: 통과
  - `session:{jti}` 삭제 후 동일 JWT 재사용 시 `401`
- `Authorization` 헤더 없는 요청: 통과
  - `POST /api/v1/gacha/pull` 헤더 없이 호출 시 `401`

## Verification

- `go build ./...` 통과
- `docker compose up -d --build`로 스택 재기동 후 HTTPS 경유 실검증
- Redis 키 직접 확인:
  - `ratelimit:172.19.0.1:POST:/api/v1/auth/steam`
  - `ratelimit:172.19.0.1:POST:/api/v1/gacha/pull`

## Notes

- [dashboard.html](/F:/stone_server/dashboard.html) 기본 상태는 이미 `T1-08: 리뷰 중`으로 설정되어 있어 추가 수정은 하지 않았습니다.
