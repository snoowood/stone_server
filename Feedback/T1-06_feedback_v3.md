# T1-06 Feedback v3

- Reviewed at: `2026-05-03`
- Task: [T1-06.md](/F:/stone_server/Task/T1-06.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: second re-review of Redis-based rate limiting middleware

## Verdict

통과. 이전 피드백에서 지적한 프록시 IP 집계 문제는 현재 검증 기준에서 재현되지 않았고, 개발 환경에서 `player_id` 기준 제한도 분리 동작을 확인했습니다.

## Checklist Review

- 한도 초과 시 `429` 응답: 통과
  - `POST /api/v1/auth/steam` 11번째 요청 `429`
  - `POST /api/v1/player/clicks` same `X-Player-ID` 기준 11번째 요청 `429`
- 1분 고정 윈도우 리셋: 기존 `v1` 검증에서 통과했고, 이번 수정은 식별 기준 보완 중심이라 회귀 징후 없음
- `auth/steam` IP 기준 적용: 통과
  - Redis 키 확인 결과: `ratelimit:172.19.0.1:POST:/api/v1/auth/steam`
- 인증 후 엔드포인트 `player_id` 기준 적용: 통과
  - Redis 키 확인 결과:
    - `ratelimit:player-aaa:POST:/api/v1/player/clicks`
    - `ratelimit:player-bbb:POST:/api/v1/player/clicks`
  - `player-aaa`는 11번째 요청 `429`, 같은 시점 `player-bbb` 첫 요청은 `200`

## Evidence

- Trusted proxy config added: [config.go](/F:/stone_server/pkg/config/config.go:1)
- Trusted proxy wiring and dev-only header injector: [main.go](/F:/stone_server/cmd/server/main.go:1)
- Dev-only `player_id` injector for verification: [stub.go](/F:/stone_server/internal/stub/stub.go:1)
- Rate limit identifier resolution unchanged but now receives trusted proxy or dev `player_id` context correctly: [ratelimit.go](/F:/stone_server/internal/middleware/ratelimit.go:1)

## Notes

- 로컬 Docker 환경에서는 `auth/steam` 키가 호스트 게이트웨이 IP `172.19.0.1`로 보였지만, 더 이상 Nginx 컨테이너 IP `172.19.0.5`로 고정되지 않았습니다.
- `player_id` 기반 검증은 개발 환경에서만 활성화되는 `X-Player-ID` 헤더 주입 미들웨어를 통해 확인했습니다. 실제 인증 미들웨어가 붙으면 같은 컨텍스트 키를 사용하면 됩니다.
