package gacha

import (
	"encoding/hex"
	"testing"
)

// TestDetermineRarity checks that each rarity is returned at the correct boundary.
func TestDetermineRarity(t *testing.T) {
	cases := []struct {
		roll   uint32
		want   Rarity
	}{
		{0, Common},
		{5999, Common},
		{6000, Uncommon},
		{8999, Uncommon},
		{9000, Rare},
		{9899, Rare},
		{9900, Unique},
		{9989, Unique},
		{9990, Legendary},
		{9999, Legendary},
	}
	for _, tc := range cases {
		got := determineRarity(tc.roll)
		if got != tc.want {
			t.Errorf("determineRarity(%d) = %q, want %q", tc.roll, got, tc.want)
		}
	}
}

// TestRoll_SeedHash verifies that the returned SeedHash is a valid 64-char hex string
// (SHA-256 produces 32 bytes = 64 hex chars) and contains no raw seed bytes.
func TestRoll_SeedHash(t *testing.T) {
	r, err := Roll()
	if err != nil {
		t.Fatalf("Roll() error: %v", err)
	}
	if len(r.SeedHash) != 64 {
		t.Errorf("SeedHash length = %d, want 64", len(r.SeedHash))
	}
	if _, err := hex.DecodeString(r.SeedHash); err != nil {
		t.Errorf("SeedHash is not valid hex: %v", err)
	}
}

// TestRoll_ItemBelongsToRarity verifies the returned ItemID is in the correct pool.
func TestRoll_ItemBelongsToRarity(t *testing.T) {
	for i := 0; i < 200; i++ {
		r, err := Roll()
		if err != nil {
			t.Fatalf("Roll() error: %v", err)
		}
		pool := itemPool[r.Rarity]
		found := false
		for _, id := range pool {
			if id == r.ItemID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ItemID %q not in pool for rarity %q", r.ItemID, r.Rarity)
		}
	}
}

// TestRollDistribution verifies 10 000 rolls land within ±2% of target probabilities.
func TestRollDistribution(t *testing.T) {
	const n = 10_000
	counts := map[Rarity]int{}
	for i := 0; i < n; i++ {
		r, err := Roll()
		if err != nil {
			t.Fatalf("Roll() error: %v", err)
		}
		counts[r.Rarity]++
	}

	targets := map[Rarity]float64{
		Common:    0.60,
		Uncommon:  0.30,
		Rare:      0.09,
		Unique:    0.009,
		Legendary: 0.001,
	}
	const tolerance = 0.02

	for rarity, target := range targets {
		got := float64(counts[rarity]) / n
		if got < target-tolerance || got > target+tolerance {
			t.Errorf("rarity %q: got %.4f, want %.4f ±%.2f", rarity, got, target, tolerance)
		}
	}
}
