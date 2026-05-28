package gacha

type Rarity string

const (
	Common    Rarity = "common"
	Uncommon  Rarity = "uncommon"
	Rare      Rarity = "rare"
	Unique    Rarity = "unique"
	Legendary Rarity = "legendary"
)

// gachaRarities lists the rarities the gacha can roll, lowest→highest.
var gachaRarities = []Rarity{Common, Uncommon, Rare, Unique, Legendary}

func isGachaRarity(r Rarity) bool {
	switch r {
	case Common, Uncommon, Rare, Unique, Legendary:
		return true
	default:
		return false
	}
}

// rarityThresholds defines cumulative cutoffs out of 10000 (0.01% granularity).
// Common 80% / Uncommon 10% / Rare 5% / Unique 4.9% / Legendary 0.1%
var rarityThresholds = []struct {
	rarity    Rarity
	threshold uint32
}{
	{Common, 8000},
	{Uncommon, 9000},
	{Rare, 9500},
	{Unique, 9990},
	{Legendary, 10000},
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

func determineRarity(roll uint32) Rarity {
	for _, t := range rarityThresholds {
		if roll < t.threshold {
			return t.rarity
		}
	}
	return Legendary
}
