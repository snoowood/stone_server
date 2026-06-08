package gacha

import (
	"context"
	"time"

	"github.com/gensdeis/stone-server/pkg/store"
	"github.com/google/uuid"
)

// item_type 값 — 클라이언트 InventorySystem.FormatItemType 과 일치해야 한다.
const (
	ItemTypeStoneSkin     = "stone_skin"
	ItemTypeWishCairnSkin = "wish_cairn_skin"
)

// source_type 값 — 클라이언트 InventorySystem.FormatSourceType 과 일치해야 한다.
const (
	SourceWishCairn = "wish_cairn"
	SourceVowReward = "vow_reward"
)

// RewardGrant 는 "스킨 1개 지급" 결과의 공통 메타데이터다. 클라이언트 RewardGrantDto
// (Assets/Scripts/Network/Dto/PlayerDto.cs) 와 JSON 필드가 1:1 로 매핑된다.
// gacha pull 과 (향후) vow 핸들러가 동일한 grant 로직을 재사용하도록 공용으로 둔다.
type RewardGrant struct {
	ItemID      string    `json:"item_id"`
	ItemType    string    `json:"item_type"`
	Rarity      string    `json:"rarity"`
	IsDuplicate bool      `json:"is_duplicate"`
	NewCount    int       `json:"new_count"`
	SourceType  string    `json:"source_type"`
	AcquiredAt  time.Time `json:"acquired_at"`
}

// rowQuerier 는 GrantItem 이 필요로 하는 store.DB / store.Tx 의 최소 부분집합이다.
// 둘 다 만족하므로 GrantItem 을 트랜잭션 안/밖 어디서나 재사용할 수 있다.
type rowQuerier interface {
	QueryRow(ctx context.Context, query string, args ...any) store.Row
}

// GrantItem 은 (player_id, item_id) UNIQUE 행에 대해 집계형 stack UPSERT 를 수행한다.
// 신규면 count=1 + item_type/source_type/acquired_at 기록, 중복이면 count += 1
// (item_type/source_type/acquired_at 은 최초 provenance 유지를 위해 갱신하지 않는다).
// RETURNING 으로 받은 count/acquired_at 으로 RewardGrant 를 구성해 돌려준다.
func GrantItem(ctx context.Context, q rowQuerier, playerID, itemID, rarity, itemType, sourceType string) (RewardGrant, error) {
	var count int
	var acquiredAt time.Time
	err := q.QueryRow(ctx, `
		INSERT INTO inventories (id, player_id, item_id, rarity, count, item_type, source_type)
		VALUES (?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT (player_id, item_id) DO UPDATE SET count = inventories.count + 1
		RETURNING count, acquired_at
	`, uuid.New().String(), playerID, itemID, rarity, itemType, sourceType).
		Scan(&count, store.ScanTime(&acquiredAt))
	if err != nil {
		return RewardGrant{}, err
	}
	return RewardGrant{
		ItemID:      itemID,
		ItemType:    itemType,
		Rarity:      rarity,
		IsDuplicate: count > 1,
		NewCount:    count,
		SourceType:  sourceType,
		AcquiredAt:  acquiredAt,
	}, nil
}
