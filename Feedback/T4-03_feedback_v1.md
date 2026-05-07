# T4-03 Feedback v1

- Task: `T4-03`
- Reviewed on: `2026-05-05`
- Verdict: `보류`

## Findings

### 1. `steam_synced:true` can be returned even when the DB still says `false`

- Severity: `P1`
- File: [internal/achievement/handler.go](/F:/stone_server/internal/achievement/handler.go:124)
- Summary: Steam API 호출이 성공하면 `synced = true`를 먼저 세팅한 뒤 `player_achievements.steam_synced = true` 업데이트를 시도합니다. 그런데 이 업데이트가 실패해도 로그만 남기고 그대로 `200 { steam_synced: true }`를 반환합니다.
- Impact: 클라이언트는 Steam 동기화가 완료됐다고 믿지만, DB에는 여전히 `steam_synced = false`가 남을 수 있습니다. 그러면 이후 업적 목록/재시도 로직과 실제 응답이 서로 어긋나는 상태가 됩니다.

## Verification

- `go test ./internal/achievement -count=1 -v`: 통과
- `go test ./...`: 통과

### Runtime checks

- 조건 충족:
  - `POST /api/v1/achievement/unlock`
  - 응답: `200 { ok: true, steam_synced: true }`
- 동일 업적 재요청:
  - 응답: `200 { ok: true, already_unlocked: true }`
- 조건 미충족 (`ACH_LEGENDARY`):
  - 응답: `400 CONDITION_NOT_MET`
- 동시 2요청 멱등성:
  - 결과: `200`, `200`
  - 응답 조합: 신규 unlock 1건 + `already_unlocked` 1건
  - DB `player_achievements` row count: `1`

### Unit-test-covered paths

- Steam API 실패 시:
  - `200 { steam_synced: false }`
  - Redis `ach:retry` 적재 확인
  - `achievement_retry_queue` insert 확인
- DB INSERT가 Steam API 호출보다 먼저 실행되는 순서 테스트 확인
- Steam 실패 후에도 `player_achievements` row가 남는 테스트 확인

## Notes

- 실제 개발 환경에서는 mock Steam client가 항상 성공해서, 위 finding은 코드 경로 리뷰 기준으로 확인했습니다.
- [dashboard.html](/F:/stone_server/dashboard.html:232) 기본 상태는 `T4-03`을 다시 `리뷰 중`으로 돌려두었습니다.
