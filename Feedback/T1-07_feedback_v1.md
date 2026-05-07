# T1-07 Feedback v1

- Reviewed at: `2026-05-04`
- Task: [T1-07.md](/F:/stone_server/Task/T1-07.md)
- Related API spec: [03_api.md](/F:/stone_server/Goal/03_api.md)
- Scope: `POST /api/v1/auth/steam`

## Verdict

보류. 순차 플로우 기준의 핵심 기능은 대부분 동작하지만, 운영에서 바로 문제가 될 수 있는 인증 설정/중복 방지 취약점이 남아 있습니다.

## Findings

1. Concurrent reuse of the same ticket is not prevented atomically.
   - 현재 구현은 먼저 `isTicketUsed()`로 조회하고, 모든 로그인 처리 후 마지막에 `storeUsedTicket()`로 사용 처리를 합니다.
   - 이 구조에서는 같은 티켓으로 동시에 들어온 두 요청이 둘 다 `used=false`를 본 뒤 각각 JWT와 refresh token을 발급받을 수 있습니다.
   - 순차 재사용은 `409 TICKET_USED`로 막히지만, 동시 재사용은 막지 못해 태스크의 anti-replay 요구사항을 완전히 충족하지 못합니다.

2. `STEAM_APP_ID` is not validated at startup.
   - production 경로에서는 `cfg.SteamAppID`를 사용해 실제 Steam `AuthenticateUserTicket` 호출을 만들지만, 필수 환경변수 검증 목록에는 `STEAM_APP_ID`가 없습니다.
   - 그 결과 운영 배포가 빈 app id로도 그냥 부팅되고, 실제 로그인은 전부 `INVALID_TICKET`처럼 보이면서 실패할 수 있습니다.

## Checklist Review

- 유효 티켓 응답 `{ jwt, refresh_token, expires_at }`: 통과
- 동일 티켓 순차 재사용 `409 TICKET_USED`: 통과
- 무효 티켓 `401 INVALID_TICKET`: 통과
- 새 로그인 시 이전 세션 jti 제거: 통과
- `players` upsert: 통과
- `player_states` 신규 레코드 생성: 통과

## Evidence

- Success path:
  - mock Steam 환경에서 `ticket-a`로 로그인 시 JWT, refresh token, expires_at 응답 확인
- Invalid ticket:
  - `invalid_ticket` 요청 시 `401`
- Duplicate sequential reuse:
  - 같은 `ticket-a` 재요청 시 `409`
- Single-session enforcement:
  - 첫 로그인 `jti1` 등록 후, 다른 티켓으로 재로그인 시 `session:current:{player_id}`가 `jti2`로 교체되고 `session:jti1`은 제거됨
- DB side effects:
  - `players` 테이블의 `steam_id=76561198000000001` row 존재 및 `last_login` 갱신 확인
  - 대응하는 `player_states` row 1건 확인

## Notes

- 이번 검증은 비프로덕션 mock Steam 클라이언트 기준으로 수행했습니다.
- 순차 플로우 기준 기능은 잘 맞아 들어가고 있어서, 위 두 이슈만 정리되면 T1-07은 완료로 올리기 좋아 보입니다.
