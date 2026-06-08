package gameconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeTemp writes content to a temp .xml and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "game-config.xml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return p
}

const validXML = `<?xml version="1.0" encoding="UTF-8"?>
<gameConfig version="1">
  <gacha>
    <pullCost>100</pullCost>
    <cooldownSeconds>1800</cooldownSeconds>
  </gacha>
  <cairn>
    <slotCount>5</slotCount>
    <maxLayers>5</maxLayers>
    <spawnIntervalSeconds>30</spawnIntervalSeconds>
  </cairn>
  <vow>
    <requiredItemCount>7</requiredItemCount>
    <tiers>
      <tier targetRarity="Uncommon">
        <minNPower>100</minNPower>
        <maxNPower>28800</maxNPower>
        <minSuccessRate>5</minSuccessRate>
        <maxSuccessRate>100</maxSuccessRate>
      </tier>
      <tier targetRarity="Rare">
        <minNPower>500</minNPower>
        <maxNPower>201600</maxNPower>
        <minSuccessRate>5</minSuccessRate>
        <maxSuccessRate>90</maxSuccessRate>
      </tier>
    </tiers>
  </vow>
</gameConfig>`

func TestLoad_Valid(t *testing.T) {
	cfg, err := Load(writeTemp(t, validXML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Version != 1 || cfg.PullCost != 100 || cfg.CooldownSeconds != 1800 ||
		cfg.SlotCount != 5 || cfg.MaxLayers != 5 || cfg.SpawnIntervalSeconds != 30 {
		t.Errorf("unexpected core values: %+v", cfg)
	}
	if cfg.VowRequiredItemCount != 7 {
		t.Errorf("VowRequiredItemCount = %d, want 7", cfg.VowRequiredItemCount)
	}
	wantTiers := []VowTier{
		{TargetRarity: "uncommon", MinNPower: 100, MaxNPower: 28800, MinSuccessRate: 5, MaxSuccessRate: 100},
		{TargetRarity: "rare", MinNPower: 500, MaxNPower: 201600, MinSuccessRate: 5, MaxSuccessRate: 90},
	}
	if !reflect.DeepEqual(cfg.VowTiers, wantTiers) {
		t.Errorf("VowTiers = %+v, want %+v", cfg.VowTiers, wantTiers)
	}
}

// The committed repo file must always be valid — guards against drift.
func TestLoad_CommittedFileIsValid(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "Data", "game-config.xml"))
	if err != nil {
		t.Fatalf("committed Data/game-config.xml failed to load: %v", err)
	}
	if cfg.Version != ExpectedVersion {
		t.Errorf("committed file version %d, expected %d", cfg.Version, ExpectedVersion)
	}
}

// validVowBlock is a well-formed <vow> block appended by mk so that core-value
// error cases fail for their intended reason, not for a missing vow block.
const validVowBlock = `<vow><requiredItemCount>7</requiredItemCount><tiers>` +
	`<tier targetRarity="Uncommon"><minNPower>100</minNPower><maxNPower>28800</maxNPower>` +
	`<minSuccessRate>5</minSuccessRate><maxSuccessRate>100</maxSuccessRate></tier></tiers></vow>`

// mk builds a full gameConfig XML (core values + a valid vow block) from value
// strings (version fixed at 1). Use raw literals for missing-key / malformed cases.
func mk(pullCost, cooldown, slot, max, interval string) string {
	return `<gameConfig version="1"><gacha><pullCost>` + pullCost +
		`</pullCost><cooldownSeconds>` + cooldown +
		`</cooldownSeconds></gacha><cairn><slotCount>` + slot +
		`</slotCount><maxLayers>` + max +
		`</maxLayers><spawnIntervalSeconds>` + interval +
		`</spawnIntervalSeconds></cairn>` + validVowBlock + `</gameConfig>`
}

// mkVow builds a full gameConfig with valid core values and a custom vow block,
// for isolating vow-specific validation cases.
func mkVow(vowBlock string) string {
	return `<gameConfig version="1"><gacha><pullCost>100</pullCost>` +
		`<cooldownSeconds>1800</cooldownSeconds></gacha><cairn><slotCount>5</slotCount>` +
		`<maxLayers>5</maxLayers><spawnIntervalSeconds>30</spawnIntervalSeconds></cairn>` +
		vowBlock + `</gameConfig>`
}

func TestLoad_Errors(t *testing.T) {
	cases := []struct {
		name string
		xml  string
	}{
		// presence / parse
		{"missing version attr", `<gameConfig><gacha><pullCost>100</pullCost><cooldownSeconds>1800</cooldownSeconds></gacha><cairn><slotCount>5</slotCount><maxLayers>5</maxLayers><spawnIntervalSeconds>30</spawnIntervalSeconds></cairn></gameConfig>`},
		{"missing pullCost key", `<gameConfig version="1"><gacha><cooldownSeconds>1800</cooldownSeconds></gacha><cairn><slotCount>5</slotCount><maxLayers>5</maxLayers><spawnIntervalSeconds>30</spawnIntervalSeconds></cairn></gameConfig>`},
		{"missing cooldown key", `<gameConfig version="1"><gacha><pullCost>100</pullCost></gacha><cairn><slotCount>5</slotCount><maxLayers>5</maxLayers><spawnIntervalSeconds>30</spawnIntervalSeconds></cairn></gameConfig>`},
		{"missing slotCount key", `<gameConfig version="1"><gacha><pullCost>100</pullCost><cooldownSeconds>1800</cooldownSeconds></gacha><cairn><maxLayers>5</maxLayers><spawnIntervalSeconds>30</spawnIntervalSeconds></cairn></gameConfig>`},
		{"missing maxLayers key", `<gameConfig version="1"><gacha><pullCost>100</pullCost><cooldownSeconds>1800</cooldownSeconds></gacha><cairn><slotCount>5</slotCount><spawnIntervalSeconds>30</spawnIntervalSeconds></cairn></gameConfig>`},
		{"missing spawnInterval key", `<gameConfig version="1"><gacha><pullCost>100</pullCost><cooldownSeconds>1800</cooldownSeconds></gacha><cairn><slotCount>5</slotCount><maxLayers>5</maxLayers></cairn></gameConfig>`},
		{"malformed xml", `<gameConfig version="1"><gacha>`},
		// non-finite / non-positive values
		{"pullCost NaN", mk("NaN", "1800", "5", "5", "30")},
		{"pullCost Inf", mk("Inf", "1800", "5", "5", "30")},
		{"pullCost zero", mk("0", "1800", "5", "5", "30")},
		{"pullCost negative", mk("-1", "1800", "5", "5", "30")},
		{"cooldown zero", mk("100", "0", "5", "5", "30")},
		{"cooldown negative", mk("100", "-60", "5", "5", "30")},
		{"slotCount zero", mk("100", "1800", "0", "5", "30")},
		{"maxLayers zero", mk("100", "1800", "5", "0", "30")},
		{"spawnInterval zero", mk("100", "1800", "5", "5", "0")},
		// unit / divisibility
		{"cooldown not multiple of 60", mk("100", "1790", "5", "5", "30")},
		{"phase not divisible", mk("100", "1800", "4", "5", "30")},
		// upper bounds (each constructed so divisibility passes first)
		{"slotCount over upper bound", mk("100", "1800", "51", "1", "51")},
		{"maxLayers over upper bound", mk("100", "1800", "1", "101", "1")},
		{"spawnInterval over upper bound", mk("100", "1800", "1", "1", "86401")},
		{"cooldown over upper bound", mk("100", "604860", "5", "5", "30")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Load(writeTemp(t, c.xml)); err == nil {
				t.Errorf("want error, got nil")
			}
		})
	}
}

func TestLoad_VowErrors(t *testing.T) {
	tier := func(rarity, minN, maxN, minR, maxR string) string {
		return `<tier targetRarity="` + rarity + `"><minNPower>` + minN +
			`</minNPower><maxNPower>` + maxN + `</maxNPower><minSuccessRate>` + minR +
			`</minSuccessRate><maxSuccessRate>` + maxR + `</maxSuccessRate></tier>`
	}
	wrap := func(reqCount, tiers string) string {
		return mkVow(`<vow><requiredItemCount>` + reqCount + `</requiredItemCount><tiers>` + tiers + `</tiers></vow>`)
	}
	ok := tier("Uncommon", "100", "28800", "5", "100")

	cases := []struct {
		name string
		xml  string
	}{
		{"missing vow block entirely", `<gameConfig version="1"><gacha><pullCost>100</pullCost><cooldownSeconds>1800</cooldownSeconds></gacha><cairn><slotCount>5</slotCount><maxLayers>5</maxLayers><spawnIntervalSeconds>30</spawnIntervalSeconds></cairn></gameConfig>`},
		{"requiredItemCount zero", wrap("0", ok)},
		{"requiredItemCount negative", wrap("-1", ok)},
		{"no tiers", wrap("7", "")},
		{"invalid rarity", wrap("7", tier("Mythic", "100", "28800", "5", "100"))},
		{"common rarity rejected", wrap("7", tier("Common", "100", "28800", "5", "100"))},
		{"duplicate rarity", wrap("7", tier("Rare", "500", "201600", "5", "90")+tier("Rare", "500", "201600", "5", "90"))},
		{"missing minNPower", wrap("7", `<tier targetRarity="Uncommon"><maxNPower>28800</maxNPower><minSuccessRate>5</minSuccessRate><maxSuccessRate>100</maxSuccessRate></tier>`)},
		{"minNPower NaN", wrap("7", tier("Uncommon", "NaN", "28800", "5", "100"))},
		{"minNPower negative", wrap("7", tier("Uncommon", "-1", "28800", "5", "100"))},
		{"maxNPower not greater than min", wrap("7", tier("Uncommon", "100", "100", "5", "100"))},
		{"successRate over 100", wrap("7", tier("Uncommon", "100", "28800", "5", "101"))},
		{"successRate negative", wrap("7", tier("Uncommon", "100", "28800", "-1", "100"))},
		{"maxSuccessRate below min", wrap("7", tier("Uncommon", "100", "28800", "90", "80"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Load(writeTemp(t, c.xml)); err == nil {
				t.Errorf("want error, got nil")
			}
		})
	}
}

// Values exactly at the upper bounds (and exact phase divisibility) must pass.
func TestLoad_BoundaryAllowed(t *testing.T) {
	// slot=50<=50, max=100<=100, interval=1<=86400; (1*100)%50==0; cooldown=604800<=604800, %60==0.
	cfg, err := Load(writeTemp(t, mk("100", "604800", "50", "100", "1")))
	if err != nil {
		t.Fatalf("boundary values should pass: %v", err)
	}
	if cfg.SlotCount != 50 || cfg.MaxLayers != 100 || cfg.CooldownSeconds != 604800 {
		t.Errorf("unexpected parse: %+v", cfg)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.xml")); err == nil {
		t.Errorf("want error for missing file, got nil")
	}
}
