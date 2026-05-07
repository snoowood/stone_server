# T2-05 Feedback v3

- Task: `T2-05`
- Reviewed on: `2026-05-05`
- Verdict: `통과`

## Findings

- 추가 findings 없음

## Verification

- `go test ./...`: 통과
- 실런타임 검증: `POST https://127.0.0.1/api/v1/player/clicks`

### Core checks

- 첫 정상 요청: `200 {"enlightenment_pts":7}`
- 같은 `batch_id` 즉시 재전송: `409 DUPLICATE_BATCH`
- Redis `click:batch:{player_id}:{batch_id}` TTL: `300`
- TTL 만료 후 같은 `batch_id` 재전송: `200 {"enlightenment_pts":14}`
- `count=0`: `400 INVALID_COUNT`
- `count=301`: `400 INVALID_COUNT`

### Rejected-request retry checks

- `TOO_FREQUENT` 재현:
  - 첫 요청: `429 TOO_FREQUENT`
  - Redis `click:batch:{player_id}:{batch_id}` 존재 여부: `0`
  - 조건 해제 후 같은 `batch_id` 재시도: `200 {"enlightenment_pts":15}`
- `RATE_EXCEEDED` 재현:
  - 첫 요청: `429 RATE_EXCEEDED`
  - Redis `click:batch:{player_id}:{batch_id}` 존재 여부: `0`
  - 조건 해제 후 같은 `batch_id` 재시도: `200 {"enlightenment_pts":16}`

### Concurrent duplicate check

- 같은 `batch_id` 동시 2요청 결과: `200,409`
- DB `enlightenment_pts`: `1.00`

## Checklist Result

- [x] 동일 `batch_id` 5분 이내 재전송 시 `409 DUPLICATE_BATCH`
- [x] 동일 `batch_id` 5분 이후 재전송 시 정상 처리
- [x] `count = 0` 시 `400 INVALID_COUNT`
- [x] `count = 301` 시 `400 INVALID_COUNT`
- [x] 시간당 3000 초과 시 `429 RATE_EXCEEDED`
- [x] 1초 미만 연속 요청 시 `429 TOO_FREQUENT`
- [x] 거절된 요청 이후 같은 `batch_id` 재시도 가능

## Notes

- 지난 두 차례 finding은 모두 해소됐습니다.
- [dashboard.html](/F:/stone_server/dashboard.html:232) 기준 기본 상태도 이미 `T2-05: 완료`로 반영돼 있습니다.
