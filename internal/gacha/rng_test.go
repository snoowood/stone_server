package gacha

import (
	"encoding/hex"
	"testing"
)

// TestDetermineRarity checks each rarity boundary for the 10000-scale table
// (Common 8000 / Uncommon 9000 / Rare 9500 / Unique 9990 / Legendary 10000).
// This deterministic check is the source of truth for drop-rate correctness.
func TestDetermineRarity(t *testing.T) {
	cases := []struct {
		roll uint32
		want Rarity
	}{
		{0, Common},
		{7999, Common},
		{8000, Uncommon},
		{8999, Uncommon},
		{9000, Rare},
		{9499, Rare},
		{9500, Unique},
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

// TestRoll_SeedHash verifies the SeedHash is a valid 64-char hex string.
func TestRoll_SeedHash(t *testing.T) {
	r, err := newTestPool(t).Roll()
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

// TestRoll_ItemBelongsToRarity verifies the returned ItemID belongs to the
// rolled rarity's bucket in the pool.
func TestRoll_ItemBelongsToRarity(t *testing.T) {
	pool := newTestPool(t)

	// rarity → set of valid skinIds (any partType).
	idsByRarity := map[Rarity]map[string]struct{}{}
	for _, byRarity := range pool.byPartRarity {
		for rarity, ids := range byRarity {
			if idsByRarity[rarity] == nil {
				idsByRarity[rarity] = map[string]struct{}{}
			}
			for _, id := range ids {
				idsByRarity[rarity][id] = struct{}{}
			}
		}
	}

	for i := 0; i < 200; i++ {
		r, err := pool.Roll()
		if err != nil {
			t.Fatalf("Roll() error: %v", err)
		}
		if _, ok := idsByRarity[r.Rarity][r.ItemID]; !ok {
			t.Errorf("ItemID %q not in pool for rarity %q", r.ItemID, r.Rarity)
		}
	}
}

// TestRollDistribution checks the four common-ish tiers land near their targets.
// Legendary (0.1%) is too rare for a meaningful absolute-tolerance check, so it
// is only asserted to stay within a loose sanity band (exactness is covered by
// the deterministic TestDetermineRarity above).
func TestRollDistribution(t *testing.T) {
	pool := newTestPool(t)
	const n = 100_000

	counts := map[Rarity]int{}
	for i := 0; i < n; i++ {
		r, err := pool.Roll()
		if err != nil {
			t.Fatalf("Roll() error: %v", err)
		}
		counts[r.Rarity]++
	}

	targets := map[Rarity]float64{
		Common:   0.80,
		Uncommon: 0.10,
		Rare:     0.05,
		Unique:   0.049,
	}
	const tolerance = 0.01
	for rarity, target := range targets {
		got := float64(counts[rarity]) / n
		if got < target-tolerance || got > target+tolerance {
			t.Errorf("rarity %q: got %.4f, want %.4f ±%.2f", rarity, got, target, tolerance)
		}
	}

	// Legendary target 0.001 → expect ~100 in 100k. Loose band to avoid flakiness.
	leg := float64(counts[Legendary]) / n
	if leg <= 0 || leg > 0.005 {
		t.Errorf("legendary rate %.5f out of sanity band (0, 0.005]", leg)
	}
}

// TestRoll_PartTypeUniform guards the core rarity→partType→skin algorithm.
// newTestPool's Common tier has partTypes {accessory(3 skins), top(1 skin)}.
// With partType chosen uniformly (1/2 each) then skin uniformly within it:
//   P(c_top1) = 1/2,  P(c_acc1) = 1/2 * 1/3 = 1/6  →  c_top1 ≈ 3× c_acc1.
// A degenerate "rarity-only uniform" pick (all 4 common skins equal) would give
// a ratio of ~1, so this fails if partType selection is dropped/broken.
func TestRoll_PartTypeUniform(t *testing.T) {
	pool := newTestPool(t)
	const n = 150_000

	counts := map[string]int{}
	for i := 0; i < n; i++ {
		r, err := pool.Roll()
		if err != nil {
			t.Fatalf("Roll() error: %v", err)
		}
		counts[r.ItemID]++
	}

	for _, id := range []string{"c_acc1", "c_acc2", "c_acc3", "c_top1"} {
		if counts[id] == 0 {
			t.Errorf("common skin %q never rolled (unreachable)", id)
		}
	}

	top, acc := counts["c_top1"], counts["c_acc1"]
	if top == 0 || acc == 0 {
		t.Fatalf("c_top1=%d c_acc1=%d, cannot compute ratio", top, acc)
	}
	ratio := float64(top) / float64(acc)
	if ratio < 2.3 || ratio > 3.7 {
		t.Errorf("c_top1/c_acc1 ratio = %.2f, want ~3 (partType-uniform); ~1 would mean partType selection is broken", ratio)
	}
}
