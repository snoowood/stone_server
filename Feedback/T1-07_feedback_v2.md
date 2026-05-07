# T1-07 Feedback v2

- Reviewed at: `2026-05-04`
- Task: [T1-07.md](/F:/stone_server/Task/T1-07.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: second review of `POST /api/v1/auth/steam`

## Verdict

보류. 이전에 지적한 동시 재사용 방지와 `STEAM_APP_ID` 필수 검증은 해결됐지만, 티켓을 Steam 검증 전에 선점하면서 무효 티켓과 일시 실패까지 `ticket:used:*`로 소모하는 문제가 새로 확인됐습니다.

## Findings

1. Invalid tickets are permanently claimed before validation.
   - 현재는 요청 초반에 `claimTicket()`으로 `ticket:used:{hash}`를 `SET NX` 한 뒤, 그 다음에 Steam 검증을 수행합니다.
   - 실검증에서도 `invalid_ticket` 첫 요청은 `401`, 같은 티켓 두 번째 요청은 `409`, 그리고 Redis에 `ticket:used:*` 키가 남는 것을 확인했습니다.
   - 이 구조에서는 잘못된 티켓도 1시간 동안 “이미 사용됨” 상태가 되고, Steam API 일시 오류가 난 경우에도 정상 재시도를 가로막을 수 있습니다.

## Re-check Summary

- 동시 동일 티켓 요청: 개선 확인
  - 같은 `race-ticket` 동시 5요청 테스트에서 `200` 1건, `409` 4건 확인
- `STEAM_APP_ID` 누락 검증: 개선 확인
  - `.env` 배제 후 `APP_ENV=production`, `STEAM_APP_ID=''`로 실행 시 즉시 `configuration error: required environment variable STEAM_APP_ID is not set in production`
- 기본 인증 플로우:
  - 유효 티켓 `200` + `{ jwt, refresh_token, expires_at }`
  - 순차 재사용 `409`
  - 새 로그인 시 이전 `jti` 제거
  - `players` upsert / `player_states` 생성 확인

## Notes

- 이번 버전은 “동시 재사용 방지” 자체는 좋아졌습니다.
- 남은 핵심은 티켓을 “검증 완료 후 확정 소모”로 볼지, 아니면 “선점 후 실패 시 롤백”으로 만들지의 정책 정리입니다.
