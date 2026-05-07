# T3-04 Feedback v1

- Task: `T3-04`
- Reviewed on: `2026-05-05`
- Verdict: `통과`

## Findings

- 추가 findings 없음

## Verification

- `go test ./internal/gacha -count=1 -v`: 통과
- 실런타임 검증: `GET /api/v1/gacha/logs`

### Pagination and ordering

- player 1 기준 `gacha_logs` 25건, 다른 player 2건을 별도로 삽입
- `GET /api/v1/gacha/logs?page=1&limit=2`
  - `200`
  - `total = 25`
  - `item_id` 순서: `item_25, item_24`
- `GET /api/v1/gacha/logs?page=2&limit=2`
  - `200`
  - `total = 25`
  - `item_id` 순서: `item_23, item_22`
- 위 결과로 `WHERE player_id`, `pulled_at DESC`, 페이지네이션 offset 동작 확인

### Default and limit guard

- `GET /api/v1/gacha/logs`
  - `200`
  - 기본 반환 개수: `20`
- `GET /api/v1/gacha/logs?limit=999`
  - `200`
  - 반환 개수: `20`
  - limit 상한 초과 시 기본값 `20` 적용 확인

### Auth

- 인증 없는 요청:
  - `401 UNAUTHORIZED`

## Notes

- 다른 플레이어 로그 2건을 함께 넣어도 `total`이 `25`로 유지되어 자기 로그만 조회되는 것도 확인했습니다.
- [dashboard.html](/F:/stone_server/dashboard.html:232) 기본 상태를 `T3-04: 완료`로 반영했습니다.
