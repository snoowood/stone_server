# T2-05 Feedback v1

- Task: `T2-05`
- Reviewed on: `2026-05-05`
- Verdict: `보류`

## Findings

### 1. Rejected click batches are still consumed as duplicates

- Severity: `P1`
- File: [internal/player/player.go](/F:/stone_server/internal/player/player.go:65)
- Summary: `checkBatchDedup()`가 `TOO_FREQUENT`와 `RATE_EXCEEDED` 검사보다 먼저 실행되어, 실제로는 처리되지 않은 요청도 `click:batch:{player_id}:{batch_id}`로 먼저 예약됩니다.
- Impact: 사용자가 1초 제한이나 시간당 제한 때문에 거절된 뒤 같은 `batch_id`로 정상 재시도해도 `409 DUPLICATE_BATCH`만 받게 됩니다. 즉, abuse 방어가 “거절 후 재시도 가능”이 아니라 “거절과 동시에 batch 소모”로 동작하고 있습니다.

## Verification

- `go test ./...`: 통과
- 실런타임 검증: `POST https://127.0.0.1/api/v1/player/clicks`

### Passed checks

- 첫 정상 요청: `200 {"enlightenment_pts":7}`
- 같은 `batch_id` 즉시 재전송: `409 DUPLICATE_BATCH`
- Redis `click:batch:{player_id}:{batch_id}` TTL: `300`
- TTL 만료를 강제로 앞당긴 뒤 같은 `batch_id` 재전송: `200 {"enlightenment_pts":14}`
- `count=0`: `400 INVALID_COUNT`
- `count=301`: `400 INVALID_COUNT`

### Failed behavior

- `TOO_FREQUENT` 재현:
  - 첫 요청: `429 TOO_FREQUENT`
  - Redis `click:batch:{player_id}:{batch_id}` 존재: `1`
  - `last-click` 조건 해제 후 같은 `batch_id` 재시도: `409 DUPLICATE_BATCH`
- `RATE_EXCEEDED` 재현:
  - 첫 요청: `429 RATE_EXCEEDED`
  - Redis `click:batch:{player_id}:{batch_id}` 존재: `1`
  - 시간당 카운터 해제 후 같은 `batch_id` 재시도: `409 DUPLICATE_BATCH`

## Checklist Result

- [x] 동일 `batch_id` 5분 이내 재전송 시 `409 DUPLICATE_BATCH`
- [x] 동일 `batch_id` 5분 이후 재전송 시 정상 처리
- [x] `count = 0` 시 `400 INVALID_COUNT`
- [x] `count = 301` 시 `400 INVALID_COUNT`
- [x] 시간당 3000 초과 시 `429 RATE_EXCEEDED`
- [x] 1초 미만 연속 요청 시 `429 TOO_FREQUENT`
- [ ] 거절된 요청 이후 같은 `batch_id`로 정상 재시도 가능

## Notes

- 순차 검증 기준으로는 대부분 요구사항이 맞습니다.
- 다만 현재 구현은 batch 예약이 너무 이른 시점에 발생해서, 제한에 걸린 요청도 처리된 batch처럼 소모되는 문제가 남아 있습니다.
