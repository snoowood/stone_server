# T2-04 Feedback v1

- Task: `T2-04`
- Reviewed on: `2026-05-05`
- Verdict: `보류`

## Findings

### 1. `/player/clicks` is still stubbed at runtime

- Severity: `P1`
- File: [cmd/server/main.go](/F:/stone_server/cmd/server/main.go:103)
- Summary: 소스상으로는 `secured.POST("/player/clicks", playerHandler.PostClicks)`가 연결되어 있지만, 실제 재빌드 후 런타임 응답은 여전히 `200 {"status":"stub"}`였습니다.
- Impact: 정상 요청이 `enlightenment_pts`를 증가시키지 않고, 검증 실패 요청도 `400`이 아닌 `200`으로 통과되어 T2-04의 핵심 요구사항이 충족되지 않습니다.

## Verification

- `go test ./...`: 통과
- `docker compose up -d --build`: 통과
- 유효 JWT로 `POST /api/v1/player/clicks` 호출:
  - Request: `{"batch_id":"11111111-1111-4111-8111-111111111111","count":7}`
  - Response: `200 {"status":"stub"}`
- 호출 전 `GET /api/v1/player/state`의 `enlightenment_pts`: `0`
- 호출 후 `GET /api/v1/player/state`의 `enlightenment_pts`: `0`
- Postgres 직접 확인:
  - `player_states.enlightenment_pts = 0.00`
- `batch_id` 누락 요청:
  - Request: `{"count":7}`
  - Response: `200 {"status":"stub"}`

## Checklist Result

- [ ] 정상 요청 시 포인트 증가 및 응답 반환
- [ ] DB `enlightenment_pts` 반영 확인
- [ ] `batch_id` 없는 요청 `400`

## Notes

- 현재 [internal/player/player.go](/F:/stone_server/internal/player/player.go:49)에는 요구사항에 맞는 핸들러 구현이 들어 있습니다.
- 하지만 실제 HTTPS 경로 검증에서는 그 구현이 실행되지 않았으므로, 런타임이 어떤 핸들러를 타는지 다시 점검이 필요합니다.
