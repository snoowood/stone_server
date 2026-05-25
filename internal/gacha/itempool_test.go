package gacha

import (
	"strings"
	"testing"
)

// TestItemPool_AllRaritiesPopulated guards against a missing-rarity regression in itemPool.
// A pull that lands on an unpopulated rarity would crash with index-out-of-range on
// rng.go's `pool[itemIdx]`.
func TestItemPool_AllRaritiesPopulated(t *testing.T) {
	rarities := []Rarity{Common, Uncommon, Rare, Unique, Legendary}
	for _, r := range rarities {
		pool, ok := itemPool[r]
		if !ok {
			t.Errorf("rarity %s missing from itemPool", r)
			continue
		}
		if len(pool) == 0 {
			t.Errorf("rarity %s has empty itemPool", r)
		}
	}
}

// TestItemPool_NoDuplicateIDs across rarities. Duplicate item_id across rarities would
// make inventories.rarity inconsistent with future Roll() outputs of the same id.
func TestItemPool_NoDuplicateIDs(t *testing.T) {
	seen := make(map[string]Rarity)
	for r, pool := range itemPool {
		for _, id := range pool {
			if id == "" {
				t.Errorf("rarity %s contains empty id", r)
				continue
			}
			if prev, exists := seen[id]; exists {
				t.Errorf("duplicate item id %q across rarities %s and %s", id, prev, r)
			}
			seen[id] = r
		}
	}
}

// TestItemPool_IDFormatSanity is a lightweight check that ids look like skin ids
// (lowercase, ascii, no whitespace). It is intentionally loose — a real match
// against the Unity SkinCatalog ids should be added once the master-data
// pipeline is in place (future milestone).
func TestItemPool_IDFormatSanity(t *testing.T) {
	for r, pool := range itemPool {
		for _, id := range pool {
			if strings.ContainsAny(id, " \t\r\n") {
				t.Errorf("rarity %s id %q contains whitespace", r, id)
			}
			if strings.ToLower(id) != id {
				t.Errorf("rarity %s id %q is not lowercase", r, id)
			}
		}
	}
}
