# T3-02 Feedback v1

- Task: `T3-02`
- Reviewed on: `2026-05-05`
- Verdict: `통과`

## Findings

- 추가 findings 없음

## Verification

- `go test ./internal/gacha -count=1 -v`: 통과
- `go test ./...`: 통과

### Runtime checks

- 정상 가챠 요청:
  - `POST /api/v1/gacha/pull` -> `200`
  - 응답 필드 확인: `item_id`, `rarity`, `is_duplicate`, `refund_points`, `next_gacha_at`
  - `player_states.enlightenment_pts`: `1000 -> 900`
  - `player_states.next_gacha_at` 갱신 확인
  - Redis `gacha:cooldown:{player_id}` 생성 확인
- 쿨다운 중 재요청:
  - `429 COOLDOWN_ACTIVE`
  - `next_gacha_at` 포함 확인
- 부족 포인트:
  - `enlightenment_pts = 50` 설정 후 요청 -> `409 INSUFFICIENT_POINTS`
- Redis miss -> DB fallback:
  - DB `next_gacha_at`만 미래 시각으로 설정하고 Redis 키 삭제
  - 재요청 -> `429 COOLDOWN_ACTIVE`
  - Redis cooldown 키 자동 복구 확인
- `gacha_logs` 기록:
  - 최신 row에서 `item_id`, `rarity`, `is_duplicate`, `refund_points`, `cost_points`, `gacha_seed_hash` 확인
  - `gacha_seed_hash` 길이 `64` 확인

### Duplicate path

- 실런타임에서 중복 아이템 케이스도 확인됨:
  - 응답: `is_duplicate: true`
  - 인벤토리에 중복 row 추가 없음 확인
  - 이번 샘플은 `common` 중복이라 `refund_points: 0`

## Notes

- 트랜잭션 중간 실패 rollback과 로그 insert 실패 rollback은 [internal/gacha/gacha_test.go](/F:/stone_server/internal/gacha/gacha_test.go:229) 단위 테스트로 커버되어 있고, 모두 통과했습니다.
- [dashboard.html](/F:/stone_server/dashboard.html:232) 기본 상태도 `T3-02: 완료`로 반영했습니다.
