# M2 Codex Re-Review (3rd round) — WishCairn 서버-gated 전환

> ⚠️ **Reminder**: 본 세션은 `--add-dir F:/stone_project` 로 클라이언트 워크스페이스가 첨부되어 있습니다. `Assets/...` 경로는 모두 `F:\stone_project\Assets\...` 입니다.

## 3rd round 추가 수정

### Fix 4b. balance override 가 Slots.Count > 0 분기 안에 있던 문제
- 위치: `F:\stone_project\Assets\Scripts\Systems\WishCairnSystem.cs:223`
- 2nd round 후 잔여: `serverSlotCount/MaxLayer/SpawnInterval` 할당이 `if (Slots.Count > 0)` 안에만 있어서 slots 가 빈 응답에서는 fallback 발생.
- 수정: balance override 를 Slots 분기 **밖** 으로 분리. `cairnState != null` 만 확인하고 양수 값들은 항상 저장. Slots 분기는 그대로 유지 (slots 자체는 비어있을 때 보존).


## 이전 라운드 FAIL 항목 → 수정 반영

### Fix 1. `GachaApiTests.cs` PullAsync 시그니처 호출 (Compile break)
- 위치: `Assets/Tests/EditMode/GachaApiTests.cs:68, :159, :175`
- 수정: 3건 모두 `GachaApi.PullAsync(0, CancellationToken.None)` (slot_index = 0) 로 갱신.

### Fix 2. `ServerStateOverridesLocalSaveTests.cs:403-407` wishCairnData 검증
- 위치: 본 테스트가 save.json 에 `wishCairnData` 키와 `slots`, `slot_1` 검증.
- M2 결정: SaveManager 가 더 이상 wishCairnData 를 채우지 않음 (서버 SSOT).
- 수정: 4) 블록 assertion 4건 제거 + 변경 사유 주석. SaveData 필드 자체는 직렬화 호환 유지.

### Fix 3. `RefreshNextStoneDropFromAnchors` stale value 보존 문제 (High)
- 위치: `Assets/Scripts/Systems/WishCairnSystem.cs` 의 `RefreshNextStoneDropFromAnchors`.
- 이전 코드: `earliest.HasValue` 일 때만 갱신 → 모든 anchor slot 이 complete 면 stale 값 유지.
- 수정: `hasAnchor` 분기 도입. anchor 가 있는 슬롯이 하나라도 있으면 `nextStoneDropAtUtc = earliest?.ToString() ?? null` 로 명시적 clear. anchor 가 전혀 없는 legacy 상태 (SaveData/LoadFromSaveData) 에서는 기존 값 보존.

### Fix 4. 클라가 서버 권위 game balance 값 사용 (High)
- 위치: `WishCairnSystem.cs` 의 `SlotCount` / `MaxLayerCount` / `StoneSpawnIntervalSeconds` property.
- 이전: GameConfig 의 값만 사용 → 서버/클라 drift 가능.
- 수정: `serverSlotCount`, `serverMaxLayerCount`, `serverSpawnIntervalSeconds` 필드 추가. `LoadFromServer` 에서 `state.Cairn.SlotCount/MaxLayers/SpawnIntervalSec` 가 양수면 저장. property 가 `server* ?? config 값` 순으로 우선순위.

### Fix 5. `LoadFromSaveData` 후 anchor 없는 슬롯이 layer 깎임 (High)
- 위치: `WishCairnSystem.cs` 의 `ComputeTargetLayer`.
- 이전: anchor 없으면 0 반환 → 다음 Update 에서 layer 0 으로 깎임.
- 수정: anchor 없거나 파싱 실패면 현재 `layerSpriteIds.Count` 반환 (no-op 으로 보존).

### 추가. EditMode 테스트 외삽 모델 호환
- `WishCairnSystemServerIntegrationTests.cs:162` `ElapsedGrowth_AddsOneLayerToIncompleteSlot`:
  - 이전: `SetNextStoneDropAtUtc(now-1s) + TryAdvanceStoneGrowth` 로 random 1 layer 추가 검증.
  - M2 외삽 모델에서는 더 이상 random 추첨이 없으므로 의미 없음.
  - 재작성: `ElapsedGrowth_DerivesLayerFromAnchor` — slot_1 의 `startedAtUtcIso` 를 60s 이전으로 세팅 → `TryAdvanceStoneGrowth` → `layers == 2` 검증.
- `WishCairnSystemServerIntegrationTests.cs:178` `FiveLayers_CompletesSlotAndAllCompleteStopsSchedule`:
  - 이전: `TryCompleteSlotForDebug` x5 + `SetNextStoneDropAtUtc(now-1s)` + `TryAdvanceStoneGrowth` → `nextStoneDropAtUtc == null` 검증.
  - M2 외삽 모델에서는 `TryAdvanceStoneGrowth` 가 anchor 기반으로 layer 를 다시 derive 해서 0 으로 깎음.
  - 재작성: `AllSlotsComplete_ClearsNextStoneDropSchedule` — 모든 슬롯의 `startedAtUtcIso` 를 200s 이전으로 세팅 → Derive 결과 모두 complete → `nextStoneDropAtUtc == null` 검증.

## 검증
- `go build ./...`: OK
- `go test ./internal/gacha/...`: PASS

## 리뷰 요청

이전 라운드의 5개 FAIL 이 적절히 해소됐는지, 그리고 새 테스트가 외삽 모델의 의도를 정확히 검증하는지 확인 부탁드립니다.

`PASS` / `FAIL` 명확히. FAIL 인 경우 (파일:라인, 무엇을, 왜).
