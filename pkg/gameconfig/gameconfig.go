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
	"os"
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
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("gameconfig: %s: missing required keys: %v", path, missing)
	}

	cfg := Config{
		Version:              *x.Version,
		PullCost:             *x.Gacha.PullCost,
		CooldownSeconds:      *x.Gacha.CooldownSeconds,
		SlotCount:            *x.Cairn.SlotCount,
		MaxLayers:            *x.Cairn.MaxLayers,
		SpawnIntervalSeconds: *x.Cairn.SpawnIntervalSeconds,
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("gameconfig: %s: %w", path, err)
	}
	return cfg, nil
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
	return nil
}
