# M4 Codex Re-Review (2nd round) — 통합 검증 + itemPool sanity

## 이전 라운드 FAIL 항목 → 수정

### Fix 1. IDFormatSanity whitespace 검증 강화
- 위치: `internal/gacha/itempool_test.go:TestItemPool_IDFormatSanity`
- 수정: `strings.TrimSpace(id) != id` → `strings.ContainsAny(id, " \t\r\n")`. 내부 공백/탭/CR/LF 도 검출.

### Fix 2. gacha 신규 에러 경로 테스트 4건 추가
- 위치: `internal/gacha/gacha_test.go`
- 추가 테스트:
  - `TestPull_MissingSlotIndex_BadRequest`: `{}` body → 400 INVALID_REQUEST.
  - `TestPull_OutOfRangeSlotIndex_BadRequest`: `slot_index=99` → 400 INVALID_SLOT.
  - `TestPull_CairnIncomplete_Forbidden`: 방금 시작된 슬롯 → 403 CAIRN_INCOMPLETE + tx rollback.
  - `TestPull_CairnSlotNotFound_NotFound`: cairn.LoadSlotStartedAt → ErrNoRows → 404 CAIRN_SLOT_NOT_FOUND + tx rollback.

### Fix 3. InitializeSlots stagger 검증
- 위치: `internal/cairn/cairn_test.go`
- 추가: `captureDB` (store.DB 구현, Exec args 캡쳐) + `TestInitializeSlots_EmitsStaggeredInserts`.
  - SlotCount 개의 INSERT, 각 args[1]=i, args[2]=`now - i × PhaseOffset` RFC3339.

> ⚠️ **Reminder**: `--add-dir F:/stone_project` 로 클라이언트 워크스페이스 첨부.

## 작업 의도

M0–M3 의 변경이 누적된 시점에서 핵심 도메인 (cairn, itemPool) 의 회귀를 막을 단위 테스트 추가. 본 마일스톤은 신규 기능 없음 — 검증 안전망 보강.

## 변경된 파일

### 서버 (`F:\stone_server`)
- 신규 `internal/cairn/cairn_test.go`:
  - `TestDerive_BuildingProgression`: 0/29/30/59/60/120/149/150/10h 의 elapsed 에 대해 `LayerCount`/`Status` 검증.
  - `TestDerive_NegativeElapsedClamped`: started_at 이 미래일 때 (clock skew) layer=0, building.
  - `TestPhaseOffset_MatchesIntendedCadence`: PhaseOffset 값 (`interval × maxLayers / slotCount`) 확인 + 슬롯별 stagger schedule (slot k 완성 시각 = `MaxLayers × interval - k × phaseOffset`).
- 신규 `internal/gacha/itempool_test.go`:
  - `TestItemPool_AllRaritiesPopulated`: 5등급 모두 ≥ 1 ID (Roll() index-out-of-range 방지).
  - `TestItemPool_NoDuplicateIDs`: 등급 간 중복 ID 없음 (inventories.rarity 일관성).
  - `TestItemPool_IDFormatSanity`: lowercase + no whitespace.
  - 주석: 서버 itemPool ID 와 클라 SkinCatalog ID 의 1:1 매칭 검증은 마스터 데이터 파이프라인 (CSV → 양쪽 빌드) 후속 마일스톤으로 남김.

### 클라이언트 (`F:\stone_project`)
- 변경 없음 — M4 는 서버 단위 테스트 보강.

## 검증
- `go test ./internal/cairn/...`: PASS
- `go test ./internal/gacha/...`: PASS

## 리뷰 요청

1. **테스트 적정성**:
   - cairn Derive 테스트 케이스가 경계 (29/30/59/60/149/150) 를 충분히 커버하는지.
   - itemPool sanity 테스트가 *방어적이지만 과하지 않은지*.
   - PhaseOffset 테스트의 stagger schedule 검증 식이 정확한지.

2. **빠진 검증**:
   - 본 마일스톤 범위 내에서 필수로 다뤄야 할 다른 회귀 위험이 있는가? (M0–M3 의 어떤 변경이 가장 약한 안전망을 가졌는가)

3. **마스터 데이터 일치 검증 deferral**:
   - 서버 itemPool ↔ 클라 SkinCatalog ID 일치 테스트는 CSV 파이프라인 후속 마일스톤으로 미뤘는데, 본 마일스톤에서 더 가벼운 대안 (예: hardcoded fixture 파일과의 단순 비교) 이 ROI 가 있는지.

`PASS` / `FAIL` 명확히. FAIL 인 경우 (파일:라인, 무엇을, 왜).
