package gacha

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

type Rarity string

const (
	Common    Rarity = "common"
	Uncommon  Rarity = "uncommon"
	Rare      Rarity = "rare"
	Unique    Rarity = "unique"
	Legendary Rarity = "legendary"
)

// rarityThresholds defines cumulative cutoffs out of 10000 (0.01% granularity).
// Common 60% / Uncommon 30% / Rare 9% / Unique 0.9% / Legendary 0.1%
var rarityThresholds = []struct {
	rarity    Rarity
	threshold uint32
}{
	{Common, 6000},
	{Uncommon, 9000},
	{Rare, 9900},
	{Unique, 9990},
	{Legendary, 10000},
}

var itemPool = map[Rarity][]string{
	Common:    {"common_stone_01", "common_stone_02", "common_stone_03"},
	Uncommon:  {"uncommon_crystal_01", "uncommon_crystal_02"},
	Rare:      {"rare_gem_01", "rare_gem_02"},
	Unique:    {"unique_relic_01"},
	Legendary: {"legendary_timepiece_01"},
}

// GameConfig holds server-side gacha parameters.
// M3: refund 폐지로 RefundPts 필드 제거. 중복 보상은 inventories.count 증가로 대체.
type GameConfig struct {
	PullCost float64
}

// DefaultConfig is the production gacha configuration.
var DefaultConfig = GameConfig{
	PullCost: 100,
}

// Result holds a single gacha pull outcome.
type Result struct {
	ItemID   string
	Rarity   Rarity
	SeedHash string // SHA-256 hex of raw seed bytes; raw seed not retained
}

// Roll performs one gacha pull using crypto/rand.
// Returns a Result containing the item, rarity, and SHA-256 seed hash.
func Roll() (Result, error) {
	// 8 bytes for rarity roll + 4 bytes for item selection within rarity pool
	seed := make([]byte, 12)
	if _, err := rand.Read(seed); err != nil {
		return Result{}, fmt.Errorf("gacha roll: %w", err)
	}

	hash := sha256.Sum256(seed)

	// rarity: first 8 bytes → uint64 mod 10000
	roll := uint32(binary.BigEndian.Uint64(seed[:8]) % 10000)
	rarity := determineRarity(roll)

	// item: last 4 bytes → index within rarity pool
	pool := itemPool[rarity]
	itemIdx := binary.BigEndian.Uint32(seed[8:]) % uint32(len(pool))

	return Result{
		ItemID:   pool[itemIdx],
		Rarity:   rarity,
		SeedHash: hex.EncodeToString(hash[:]),
	}, nil
}

func determineRarity(roll uint32) Rarity {
	for _, t := range rarityThresholds {
		if roll < t.threshold {
			return t.rarity
		}
	}
	return Legendary
}
