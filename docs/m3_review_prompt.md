# M3 Codex Review — 인벤토리 집계형 (count) 도입

> ⚠️ **Reminder**: `--add-dir F:/stone_project` 로 클라이언트 워크스페이스 첨부. `Assets/...` 경로는 모두 `F:\stone_project\Assets\...`.

## 작업 의도

사용자 결정 (F1: 집계형) 에 따라 가챠 중복 아이템을 **단일 행의 count 누적** 으로 처리. refund 폐지 — 중복 보상은 stack 자체가 가치.

## 변경된 파일

### 서버 (`F:\stone_server`)
- 신규: `migrations/000012_inventory_count.{up,down}.sql` — `inventories.count INT NOT NULL DEFAULT 1`.
- `pkg/sqlitedb/sqlitedb.go`: schema 에 `count` 컬럼 추가 + reused SQLite DB 용 best-effort `ALTER ADD COLUMN` cleanup.
- `internal/gacha/gacha.go`:
  - `pullResponse` 에 `NewCount int` 추가. `RefundPoints` 는 호환성 유지 (항상 0).
  - `execPull` 의 inventory INSERT → `INSERT ... ON CONFLICT DO UPDATE SET count = inventories.count + 1 RETURNING count` (tx.QueryRow). `isDuplicate = newCount > 1` 로 판정.
  - 기존 refund UPDATE 분기 제거. `refundPts := 0.0` 만 유지 (응답 호환).
- `internal/gacha/rng.go`: `GameConfig.RefundPts` 필드 + `DefaultConfig.RefundPts` 제거 (orphan).
- `internal/player/player.go`: `inventoryItem` 에 `Count int` 필드 추가, `SELECT` 에 `count` 컬럼 포함.
- `internal/gacha/gacha_test.go`:
  - 헬퍼 `rowIntResult`, `rowErrResult` 추가.
  - 영향 받은 4개 테스트 큐 재구성: inventory 가 QueryRow 로 이동했으므로 queryRowQueue 에 RETURNING count 응답 추가, execQueue 에서 inventory exec 제거.
  - `TestExecPull_DuplicateItem_Refund` → `TestExecPull_DuplicateItem_StackIncreases` 로 재작성 (cfg override 제거, `new_count:3`, `refund_points:0`, `balance_after:400` 검증).

### 클라이언트 (`F:\stone_project`)
- `Assets/Scripts/Network/Dto/PlayerDto.cs`: `InventoryItemDto.Count` 필드 추가.
- `Assets/Scripts/Network/Dto/GachaDto.cs`: `GachaPullResponseDto.NewCount` 필드 추가.
- `Assets/Scripts/Systems/InventorySystem.cs`:
  - `InventoryItemData.count` 필드 추가.
  - `LoadFromServer`: 1:1 매핑하되 count fallback `s.Count > 0 ? s.Count : 1`.
  - `AddItem(skinId, sourceType, int newCount = 0)`: 같은 skinId 존재시 count 증가 (newCount > 0 면 절대값, 아니면 +1). 없으면 신규 행 (count = newCount > 0 ? newCount : 1).
  - `GetItemCountBySkinId`: `Count(rows)` → `Sum(count)`.
  - `GetGroupedSlots`: 같은 변경.
- `Assets/Scripts/Systems/WishCairnSystem.cs`:
  - `ApplySuccessfulClaim`: `if (!dto.IsDuplicate)` 분기 제거 → 항상 `AddItem(itemId, source, dto.NewCount)`.
  - offline reward DTO: `NewCount = 0` (AddItem fallback +1 사용).
- 테스트:
  - `Fixtures/player_state.json`: inventory 항목에 `count` 추가 (1, 3).
  - `PlayerDtoTests`: count 검증 추가.
  - `GachaDtoTests.GachaPullResponseDto_DeserializesServerJson`: `new_count` 검증 추가.

## 검증
- `go build ./...`: OK
- `go test ./internal/gacha/...`: PASS

## 리뷰 요청

1. **사실관계 / blocking issue**:
   - `INSERT ... ON CONFLICT DO UPDATE SET count = inventories.count + 1 RETURNING count` 가 SQLite/PG 양쪽에서 의도대로 동작하는지 (modernc.org/sqlite 3.4x 의 RETURNING + ON CONFLICT DO UPDATE 지원).
   - `isDuplicate = newCount > 1` 판정이 race condition 안전한지 (트랜잭션 안에서 RETURNING 이라 atomic).
   - 클라 `InventorySystem.AddItem` 의 newCount 기본값 0 시 fallback 동작 (`+1` 누적) 이 적절한지.
   - `WishCairnSystem` 의 offline reward 가 `NewCount = 0` 으로 호출 → 같은 skinId 가 있으면 +1, 없으면 1 신규. 의도된 동작인지.

2. **테스트 호환성**:
   - SystemsLoadFromServerTests `InventorySystem_LoadFromServer_ReplacesItemsWithServerInventory`: InventoryItemDto.Count 미지정 (=0) 시 LoadFromServer 가 1 로 fallback → items.Count == 2 검증 통과 예상.

3. **추가 정리**:
   - 다른 PlayMode fixture (NetworkBootstrap, Phase1EndToEnd 등) 의 inventory 응답에 count 누락 시 디시리얼라이즈 동작.

`PASS` / `FAIL` 명확히. FAIL 인 경우 (파일:라인, 무엇을, 왜).
