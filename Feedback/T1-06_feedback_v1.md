# T1-06 Feedback v1

- Reviewed at: `2026-05-03`
- Task: [T1-06.md](/F:/stone_server/Task/T1-06.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: Redis-based rate limiting middleware

## Verdict

보류. `429` 응답과 1분 윈도우 리셋 자체는 동작하지만, 현재 배포 경로에서는 제한 키가 실제 클라이언트나 `player_id`가 아니라 Nginx 프록시 IP 하나로 묶이고 있어 요구한 식별 기준을 충족하지 못합니다.

## Findings

1. All rate limits currently collapse to the reverse proxy IP.
   - 실검증에서 `POST /api/v1/auth/steam` 11회 호출 시 11번째에 `429`는 발생했지만, Redis 키가 `ratelimit:172.19.0.5:POST:/api/v1/auth/steam`로 생성됐습니다.
   - `POST /api/v1/gacha/pull`도 마찬가지로 `ratelimit:172.19.0.5:POST:/api/v1/gacha/pull` 키를 사용했습니다.
   - 즉 현재는 실제 사용자 IP도 아니고 `player_id`도 아니며, Nginx 컨테이너 주소 하나로 모든 요청이 합산됩니다.

## Checklist Review

- 한도 초과 시 `429` 응답: 통과
  - `POST /api/v1/auth/steam` 11번째 요청 `429`
  - `POST /api/v1/gacha/pull` 6번째 요청 `429`
- 1분 경과 후 카운터 리셋: 통과
  - Redis TTL `60` 확인 후 61초 대기 뒤 동일 요청 `200`
- `auth/steam` IP 기준, 인증 후 엔드포인트 `player_id` 기준 적용: 실패
  - 현재 배포 경로에서는 둘 다 프록시 IP 하나로 합산됨

## Evidence

- Middleware implementation: [ratelimit.go](/F:/stone_server/internal/middleware/ratelimit.go:1)
- Global middleware wiring: [main.go](/F:/stone_server/cmd/server/main.go:55)
- Test routes used for verification: [stub.go](/F:/stone_server/internal/stub/stub.go:1)

## Notes

- 현재 구현은 `player_id`가 컨텍스트에 없으면 IP로 fallback 하도록 되어 있는데, 지금 앱은 Nginx 뒤에서 `c.ClientIP()`가 실제 사용자 IP가 아니라 프록시 IP로 해석되고 있습니다.
- T1-07 이후 인증 미들웨어가 붙더라도, 프록시 신뢰 설정이 해결되지 않으면 IP 기반 정책은 계속 잘못 집계될 가능성이 큽니다.
