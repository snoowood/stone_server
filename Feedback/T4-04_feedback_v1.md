# T4-04 Feedback v1

- Task: `T4-04`
- Reviewed on: `2026-05-05`
- Verdict: `통과`

## Findings

- 추가 findings 없음

## Verification

- `go test ./internal/achievement -count=1 -v`: 통과
- `go test ./...`: 통과

### Runtime checks

- `GET /api/v1/achievement/list` with JWT:
  - `200`
  - 전체 6개 업적 반환 확인
  - 순서: `ACH_FIRST_PULL, ACH_RARE_UNLOCK, ACH_LEGENDARY, ACH_STREAK_7, ACH_STREAK_30, ACH_COLLECTOR`
- unlocked 상태 확인:
  - `ACH_FIRST_PULL`: `unlocked = true`, `unlocked_at != null`, `steam_synced = true`
  - `ACH_COLLECTOR`: `unlocked = true`, `unlocked_at != null`, `steam_synced = false`
- 미달성 업적 확인:
  - `ACH_LEGENDARY`: `unlocked = false`, `unlocked_at = null`, `steam_synced = false`
- 인증 없는 요청:
  - `401 UNAUTHORIZED`

### Unit test coverage

- 빈 결과셋에서도 6개 전체 업적 목록 반환 확인
- 부분 unlock 상태 merge 확인
- 응답 순서가 서버 정의 `allAchievements`와 일치하는지 확인

## Notes

- 태스크 요구사항인 “전체 6개 업적 포함”, “달성 업적 unlocked/unlocked_at”, “미달성 업적 기본값”, “steam_synced 정확 반환”을 모두 충족했습니다.
- [dashboard.html](/F:/stone_server/dashboard.html:232) 기준 기본 상태도 이미 `T4-04: 완료`로 반영돼 있습니다.
