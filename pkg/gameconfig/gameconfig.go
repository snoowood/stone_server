// Package gameconfig loads the shared client/server game balance values from
// game-config.xml (single source synced from stone_project; see
// docs/plans/game-config-xml-schema.md).
//
// The loader fails fast on malformed/out-of-range values. Version mismatch is a
// warn-and-proceed signal handled by the caller (see ExpectedVersion).
package gameconfig

import (
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"strings"
)

// ExpectedVersion is the schema version this build was written against.
// A different version in the file is logged as a warning by the caller (R3:
// 경고 후 진행) — structural breakage is caught by the missing-key check below.
const ExpectedVersion = 1

// Config is the validated, immutable game configuration cache.
type Config struct {
	Version              int
	PullCost             float64
	CooldownSeconds      int
	SlotCount            int
	MaxLayers            int
	SpawnIntervalSeconds int
	VowRequiredItemCount int
	VowTiers             []VowTier
}

// VowTier is one validated entry of the Vow crafting N-power/success-rate table,
// keyed by the reward rarity a successful Vow targets.
type VowTier struct {
	TargetRarity   string // lowercased: uncommon|rare|unique|legendary
	MinNPower      float64
	MaxNPower      float64
	MinSuccessRate float64 // percent, 0..100
	MaxSuccessRate float64 // percent, 0..100
}

// xmlConfig uses pointer fields so a missing key reads as nil (distinguishable
// from an explicit 0) — see schema §6 누락 검출.
type xmlConfig struct {
	XMLName xml.Name `xml:"gameConfig"`
	Version *int     `xml:"version,attr"`
	Gacha   struct {
		PullCost        *float64 `xml:"pullCost"`
		CooldownSeconds *int     `xml:"cooldownSeconds"`
	} `xml:"gacha"`
	Cairn struct {
		SlotCount            *int `xml:"slotCount"`
		MaxLayers            *int `xml:"maxLayers"`
		SpawnIntervalSeconds *int `xml:"spawnIntervalSeconds"`
	} `xml:"cairn"`
	Vow struct {
		RequiredItemCount *int `xml:"requiredItemCount"`
		Tiers             struct {
			Tier []xmlVowTier `xml:"tier"`
		} `xml:"tiers"`
	} `xml:"vow"`
}

type xmlVowTier struct {
	TargetRarity   string   `xml:"targetRarity,attr"`
	MinNPower      *float64 `xml:"minNPower"`
	MaxNPower      *float64 `xml:"maxNPower"`
	MinSuccessRate *float64 `xml:"minSuccessRate"`
	MaxSuccessRate *float64 `xml:"maxSuccessRate"`
}

// Load reads, parses, and validates the config at path. fail-fast on any error.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("gameconfig: read %s: %w", path, err)
	}

	var x xmlConfig
	if err := xml.Unmarshal(data, &x); err != nil {
		return Config{}, fmt.Errorf("gameconfig: parse %s: %w", path, err)
	}

	missing := make([]string, 0, 6)
	if x.Version == nil {
		missing = append(missing, "version")
	}
	if x.Gacha.PullCost == nil {
		missing = append(missing, "gacha.pullCost")
	}
	if x.Gacha.CooldownSeconds == nil {
		missing = append(missing, "gacha.cooldownSeconds")
	}
	if x.Cairn.SlotCount == nil {
		missing = append(missing, "cairn.slotCount")
	}
	if x.Cairn.MaxLayers == nil {
		missing = append(missing, "cairn.maxLayers")
	}
	if x.Cairn.SpawnIntervalSeconds == nil {
		missing = append(missing, "cairn.spawnIntervalSeconds")
	}
	if x.Vow.RequiredItemCount == nil {
		missing = append(missing, "vow.requiredItemCount")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("gameconfig: %s: missing required keys: %v", path, missing)
	}

	tiers, err := buildVowTiers(x.Vow.Tiers.Tier)
	if err != nil {
		return Config{}, fmt.Errorf("gameconfig: %s: %w", path, err)
	}

	cfg := Config{
		Version:              *x.Version,
		PullCost:             *x.Gacha.PullCost,
		CooldownSeconds:      *x.Gacha.CooldownSeconds,
		SlotCount:            *x.Cairn.SlotCount,
		MaxLayers:            *x.Cairn.MaxLayers,
		SpawnIntervalSeconds: *x.Cairn.SpawnIntervalSeconds,
		VowRequiredItemCount: *x.Vow.RequiredItemCount,
		VowTiers:             tiers,
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("gameconfig: %s: %w", path, err)
	}
	return cfg, nil
}

// validVowRarities are the reward rarities a Vow may target (Common excluded —
// mirrors the client loader's Uncommon..Legendary range). Keyed lowercase.
var validVowRarities = map[string]bool{
	"uncommon":  true,
	"rare":      true,
	"unique":    true,
	"legendary": true,
}

// buildVowTiers parses and validates the <tier> entries, fail-fast on any problem
// (server policy; the client loader falls back to defaults instead). Tiers are
// returned in document order — the committed file authors them lowest→highest.
func buildVowTiers(raw []xmlVowTier) ([]VowTier, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("vow.tiers must have at least one tier")
	}
	tiers := make([]VowTier, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, t := range raw {
		rarity := strings.ToLower(strings.TrimSpace(t.TargetRarity))
		if !validVowRarities[rarity] {
			return nil, fmt.Errorf("vow tier targetRarity must be one of uncommon/rare/unique/legendary, got %q", t.TargetRarity)
		}
		if seen[rarity] {
			return nil, fmt.Errorf("duplicate vow tier targetRarity: %q", rarity)
		}
		seen[rarity] = true

		if t.MinNPower == nil || t.MaxNPower == nil || t.MinSuccessRate == nil || t.MaxSuccessRate == nil {
			return nil, fmt.Errorf("vow tier %q missing required numeric keys", rarity)
		}
		tier := VowTier{
			TargetRarity:   rarity,
			MinNPower:      *t.MinNPower,
			MaxNPower:      *t.MaxNPower,
			MinSuccessRate: *t.MinSuccessRate,
			MaxSuccessRate: *t.MaxSuccessRate,
		}
		if err := tier.validate(); err != nil {
			return nil, err
		}
		tiers = append(tiers, tier)
	}
	return tiers, nil
}

func (t VowTier) validate() error {
	// NaN/Inf slip past ordering comparisons, so reject non-finite first.
	for _, v := range []struct {
		name string
		val  float64
	}{
		{"minNPower", t.MinNPower}, {"maxNPower", t.MaxNPower},
		{"minSuccessRate", t.MinSuccessRate}, {"maxSuccessRate", t.MaxSuccessRate},
	} {
		if math.IsNaN(v.val) || math.IsInf(v.val, 0) {
			return fmt.Errorf("vow tier %q %s must be finite, got %v", t.TargetRarity, v.name, v.val)
		}
	}
	if t.MinNPower < 0 {
		return fmt.Errorf("vow tier %q minNPower must be >= 0, got %v", t.TargetRarity, t.MinNPower)
	}
	if t.MaxNPower <= t.MinNPower {
		return fmt.Errorf("vow tier %q maxNPower (%v) must be > minNPower (%v)", t.TargetRarity, t.MaxNPower, t.MinNPower)
	}
	if t.MinSuccessRate < 0 || t.MinSuccessRate > 100 || t.MaxSuccessRate < 0 || t.MaxSuccessRate > 100 {
		return fmt.Errorf("vow tier %q success rates must be in 0..100, got min=%v max=%v", t.TargetRarity, t.MinSuccessRate, t.MaxSuccessRate)
	}
	if t.MaxSuccessRate < t.MinSuccessRate {
		return fmt.Errorf("vow tier %q maxSuccessRate (%v) must be >= minSuccessRate (%v)", t.TargetRarity, t.MaxSuccessRate, t.MinSuccessRate)
	}
	return nil
}

// Upper bounds are operational guardrails against extreme values blowing up
// InitializeSlots loops / response sizes. Adjust if balance ever needs more.
const (
	maxSlotCount            = 50
	maxMaxLayers            = 100
	maxSpawnIntervalSeconds = 86400  // 1 day
	maxCooldownSeconds      = 604800 // 1 week
)

func (c Config) validate() error {
	// NaN/Inf slip past "<= 0" comparisons (ParseFloat accepts "NaN"/"Inf"), so
	// reject non-finite first — keeps parity with the client loader.
	if math.IsNaN(c.PullCost) || math.IsInf(c.PullCost, 0) {
		return fmt.Errorf("pullCost must be finite, got %v", c.PullCost)
	}
	if c.PullCost <= 0 {
		return fmt.Errorf("pullCost must be > 0, got %v", c.PullCost)
	}
	if c.CooldownSeconds <= 0 {
		return fmt.Errorf("cooldownSeconds must be > 0, got %d", c.CooldownSeconds)
	}
	// Client renders cooldown in minutes; non-multiples of 60 would truncate.
	if c.CooldownSeconds%60 != 0 {
		return fmt.Errorf("cooldownSeconds must be a multiple of 60, got %d", c.CooldownSeconds)
	}
	if c.SlotCount <= 0 {
		return fmt.Errorf("slotCount must be > 0, got %d", c.SlotCount)
	}
	if c.MaxLayers <= 0 {
		return fmt.Errorf("maxLayers must be > 0, got %d", c.MaxLayers)
	}
	if c.SpawnIntervalSeconds <= 0 {
		return fmt.Errorf("spawnIntervalSeconds must be > 0, got %d", c.SpawnIntervalSeconds)
	}
	// PhaseOffset = spawnIntervalSeconds*maxLayers/slotCount is integer division;
	// a non-exact quotient silently shifts slot phase. Require exact divisibility.
	if (c.SpawnIntervalSeconds*c.MaxLayers)%c.SlotCount != 0 {
		return fmt.Errorf("spawnIntervalSeconds*maxLayers (%d) must be divisible by slotCount (%d)",
			c.SpawnIntervalSeconds*c.MaxLayers, c.SlotCount)
	}
	if c.SlotCount > maxSlotCount {
		return fmt.Errorf("slotCount must be <= %d, got %d", maxSlotCount, c.SlotCount)
	}
	if c.MaxLayers > maxMaxLayers {
		return fmt.Errorf("maxLayers must be <= %d, got %d", maxMaxLayers, c.MaxLayers)
	}
	if c.SpawnIntervalSeconds > maxSpawnIntervalSeconds {
		return fmt.Errorf("spawnIntervalSeconds must be <= %d, got %d", maxSpawnIntervalSeconds, c.SpawnIntervalSeconds)
	}
	if c.CooldownSeconds > maxCooldownSeconds {
		return fmt.Errorf("cooldownSeconds must be <= %d, got %d", maxCooldownSeconds, c.CooldownSeconds)
	}
	if c.VowRequiredItemCount <= 0 {
		return fmt.Errorf("vow.requiredItemCount must be > 0, got %d", c.VowRequiredItemCount)
	}
	return nil
}
