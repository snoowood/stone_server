# T1-06 Feedback v2

- Reviewed at: `2026-05-03`
- Task: [T1-06.md](/F:/stone_server/Task/T1-06.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: re-review of Redis-based rate limiting middleware

## Verdict

보류. 이전 리뷰에서 지적한 프록시 IP 집계 문제가 현재 코드와 실검증 기준으로 그대로 재현됩니다.

## Re-check Result

- `POST /api/v1/auth/steam`
  - 11번째 요청에서 `429` 발생
  - Redis 키: `ratelimit:172.19.0.5:POST:/api/v1/auth/steam`
- `POST /api/v1/gacha/pull`
  - 6번째 요청에서 `429` 발생
  - Redis 키: `ratelimit:172.19.0.5:POST:/api/v1/gacha/pull`

## Finding Status

- 기존 Finding 유지: 모든 요청이 실제 클라이언트나 `player_id`가 아니라 Nginx 프록시 IP `172.19.0.5` 기준으로 집계됨

## Evidence

- Rate limit identifier resolution: [ratelimit.go](/F:/stone_server/internal/middleware/ratelimit.go:71)
- App proxy trust setting: [main.go](/F:/stone_server/cmd/server/main.go:56)
- Current test routes still do not set `player_id`: [stub.go](/F:/stone_server/internal/stub/stub.go:9)

## Notes

- 이번 재리뷰 시점에는 지난 피드백 이후 핵심 수정 흔적이 확인되지 않았습니다.
- 따라서 T1-06은 여전히 완료 처리하기 어렵고, 프록시 신뢰 설정과 `player_id` 기반 집계 경로가 실제로 동작하도록 수정된 뒤 다시 검증이 필요합니다.
