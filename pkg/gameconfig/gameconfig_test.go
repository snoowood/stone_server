package gameconfig

import (
	"os"
	"path/filepath"
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
</gameConfig>`

func TestLoad_Valid(t *testing.T) {
	cfg, err := Load(writeTemp(t, validXML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{Version: 1, PullCost: 100, CooldownSeconds: 1800, SlotCount: 5, MaxLayers: 5, SpawnIntervalSeconds: 30}
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
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

// mk builds a full gameConfig XML from value strings (version fixed at 1).
// Use raw literals for missing-key / malformed cases.
func mk(pullCost, cooldown, slot, max, interval string) string {
	return `<gameConfig version="1"><gacha><pullCost>` + pullCost +
		`</pullCost><cooldownSeconds>` + cooldown +
		`</cooldownSeconds></gacha><cairn><slotCount>` + slot +
		`</slotCount><maxLayers>` + max +
		`</maxLayers><spawnIntervalSeconds>` + interval +
		`</spawnIntervalSeconds></cairn></gameConfig>`
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
