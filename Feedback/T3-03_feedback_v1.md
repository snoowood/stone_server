# T3-03 Feedback v1

- Task: `T3-03`
- Reviewed on: `2026-05-05`
- Verdict: `통과`

## Findings

- 추가 findings 없음

## Verification

- `go test ./internal/gacha -count=1 -v`: 통과
- `GET /api/v1/gacha/status` 실런타임 검증:
  - 쿨다운 없음:
    - DB `next_gacha_at = NULL`, `pity_count = 7`
    - 응답: `200 {"can_pull":true,"next_gacha_at":null,"pity_count":7}`
  - 쿨다운 활성화(Redis hit):
    - Redis `gacha:cooldown:{player_id}` 미래 시각 설정, DB `pity_count = 3`
    - 응답: `200 {"can_pull":false,"next_gacha_at":"...","pity_count":3}`
  - Redis miss -> DB fallback:
    - DB `next_gacha_at`만 미래 시각으로 설정 후 Redis 키 삭제
    - 응답: `200 {"can_pull":false,"next_gacha_at":"...","pity_count":2}`
    - Redis cooldown 키 자동 복구 확인
- 인증 없는 요청:
  - `401 UNAUTHORIZED`

## Notes

- 체크리스트의 세 가지 핵심 조건인 `can_pull=false + next_gacha_at`, `can_pull=true + next_gacha_at:null`, Redis miss 시 DB fallback 동작을 모두 확인했습니다.
- [dashboard.html](/F:/stone_server/dashboard.html:232) 기본 상태도 `T3-03: 완료`로 반영했습니다.
