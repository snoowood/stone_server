# T2-05 Feedback v2

- Task: `T2-05`
- Reviewed on: `2026-05-05`
- Verdict: `보류`

## Findings

### 1. Immediate duplicate batches are masked as `TOO_FREQUENT` instead of `DUPLICATE_BATCH`

- Severity: `P1`
- File: [internal/player/player.go](/F:/stone_server/internal/player/player.go:65)
- Summary: 이전 finding은 해소되어 `TOO_FREQUENT`와 `RATE_EXCEEDED`로 거절된 요청이 더는 batch key를 선점하지 않습니다. 하지만 현재는 검사 순서가 `TOO_FREQUENT` -> `RATE_EXCEEDED` -> `DUPLICATE_BATCH`라서, 같은 `batch_id`를 즉시 재전송하면 `409 DUPLICATE_BATCH`가 아니라 `429 TOO_FREQUENT`가 먼저 반환됩니다.
- Impact: 태스크 체크리스트의 “동일 `batch_id` 5분 이내 재전송 시 `409 DUPLICATE_BATCH`”가 즉시 재전송 케이스에서는 성립하지 않습니다. 실제로는 1초가 지난 뒤에야 같은 batch가 `409`로 보입니다.

## Verification

- `go test ./...`: 통과
- 실런타임 검증: `POST https://127.0.0.1/api/v1/player/clicks`

### Confirmed fixed from v1

- `TOO_FREQUENT` 거절 요청 후 Redis `click:batch:{player_id}:{batch_id}` 존재 여부: `0`
- 위 조건 해제 후 같은 `batch_id` 재시도: `200 {"enlightenment_pts":15}`
- `RATE_EXCEEDED` 거절 요청 후 Redis `click:batch:{player_id}:{batch_id}` 존재 여부: `0`
- 위 조건 해제 후 같은 `batch_id` 재시도: `200 {"enlightenment_pts":16}`

### Remaining issue

- 첫 정상 요청: `200 {"enlightenment_pts":7}`
- 같은 `batch_id` 즉시 재전송: `429 TOO_FREQUENT`
- 같은 `batch_id`를 2초 뒤 재전송: `409 DUPLICATE_BATCH`
- 동일 `batch_id` TTL 강제 만료 후 재전송: `200 {"enlightenment_pts":14}`

### Other checks

- `count=0`: `400 INVALID_COUNT`
- `count=301`: `400 INVALID_COUNT`

## Checklist Result

- [ ] 동일 `batch_id` 5분 이내 재전송 시 `409 DUPLICATE_BATCH`
- [x] 동일 `batch_id` 5분 이후 재전송 시 정상 처리
- [x] `count = 0` 시 `400 INVALID_COUNT`
- [x] `count = 301` 시 `400 INVALID_COUNT`
- [x] 시간당 3000 초과 시 `429 RATE_EXCEEDED`
- [x] 1초 미만 연속 요청 시 `429 TOO_FREQUENT`
- [x] 거절된 요청 이후 같은 `batch_id` 재시도 가능

## Notes

- 지난 v1 finding은 분명히 해소됐습니다.
- 현재 남은 이슈는 rule 우선순위 하나이며, duplicate 판정을 더 앞세울지 현재 동작을 태스크 기대와 맞춰 조정할지가 필요합니다.
