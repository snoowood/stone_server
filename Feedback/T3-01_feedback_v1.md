# T3-01 Feedback v1

- Task: `T3-01`
- Reviewed on: `2026-05-05`
- Verdict: `보류`

## Findings

### 1. Gacha rarity values use title case instead of the documented lowercase contract

- Severity: `P1`
- File: [internal/gacha/rng.go](/F:/stone_server/internal/gacha/rng.go:14)
- Summary: 현재 `Rarity` 상수는 `"Common"`, `"Uncommon"`, `"Rare"`, `"Unique"`, `"Legendary"`로 정의돼 있습니다.
- Impact: DB/쿼리/API 문서는 모두 `common/uncommon/rare/unique/legendary` 소문자 규약을 전제로 합니다. 이 값이 그대로 `inventories.rarity`, `gacha_logs.rarity`, API 응답, 또는 이후 업적 조건 쿼리에 흘러가면 T3-02 이후 중복 판정, 업적 unlock, 클라이언트 비교 로직이 어긋날 가능성이 큽니다.

## Verification

- `go test ./internal/gacha -count=1 -v`: 통과
- `go test ./...`: 통과
- `crypto/rand` 사용 확인: 통과
- `math/rand` 사용 없음 확인: 통과
- `SeedHash` SHA-256 64자리 hex 형식 확인: 통과
- rarity 경계값 테스트(`0, 5999, 6000, ... 9999`): 통과
- 분포 테스트(10,000회, 목표 비율 ±2% 범위): 통과

## Contract references

- DB 설계: [Goal/02_database.md](/F:/stone_server/Goal/02_database.md:60)
  - `rarity -- common/uncommon/rare/unique/legendary`
- API 예시: [Goal/03_api.md](/F:/stone_server/Goal/03_api.md:148), [Goal/03_api.md](/F:/stone_server/Goal/03_api.md:189)
  - inventory rarity: `"rare"`
  - gacha rarity: `"legendary"`

## Notes

- RNG 구현 자체는 좋습니다. `crypto/rand` 기반, 시드 비노출, SHA-256 해시 반환, 희귀도 테이블과 아이템 매핑, 기본 환급 포인트 테이블까지 모두 들어가 있습니다.
- 현재 보류 사유는 주로 문자열 규약 불일치 1건입니다.
