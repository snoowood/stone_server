package gacha

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newTestPool builds a valid pool covering all 5 rarities, with Common split
// across two partTypes of UNEQUAL skin counts (accessory×3, top×1). That shape
// lets TestRoll_PartTypeUniform distinguish the real "partType uniform" algorithm
// (c_top1 ≈ 3× c_acc1) from a degenerate "rarity-only uniform" one (≈ equal).
// Shared by gacha_test.go and rng_test.go.
func newTestPool(t *testing.T) *SkinPool {
	t.Helper()
	const fixture = `skinId,partType,rarity
c_acc1,Accessory,Common
c_acc2,Accessory,Common
c_acc3,Accessory,Common
c_top1,Top,Common
u_acc1,Accessory,Uncommon
u_top1,Top,Uncommon
r_acc1,Accessory,Rare
r_top1,Top,Rare
q_acc1,Accessory,Unique
l_acc1,Accessory,Legendary
`
	p, err := parseSkinPool(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("newTestPool: %v", err)
	}
	return p
}

// TestParseSkinPool_Valid: 모든 등급 채움, 정렬, 스킵 0, 통계 불변식.
func TestParseSkinPool_Valid(t *testing.T) {
	const csv = `skinId,partType,rarity
a,Accessory,Common
b,Top,Common
c,Outfit,Uncommon
d,Face,Rare
e,Effect,Unique
f,Bottom,Legendary
`
	p, err := parseSkinPool(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parseSkinPool: %v", err)
	}

	for _, r := range gachaRarities {
		if len(p.partsByRarity[r]) == 0 {
			t.Errorf("rarity %q has no partTypes", r)
		}
	}
	// Common 은 accessory, top 두 partType → 정렬 확인.
	got := p.partsByRarity[Common]
	if len(got) != 2 || got[0] != "accessory" || got[1] != "top" {
		t.Errorf("partsByRarity[Common] = %v, want [accessory top]", got)
	}

	st := p.Stats()
	if st.Total != 6 || st.Loaded != 6 {
		t.Errorf("stats Total/Loaded = %d/%d, want 6/6", st.Total, st.Loaded)
	}
	if st.SkippedBadRarity+st.SkippedBadPart+st.SkippedMalformed != 0 {
		t.Errorf("unexpected skips: %+v", st)
	}
}

// TestParseSkinPool_DuplicateSkinId: 중복 skinId 는 fail-fast.
func TestParseSkinPool_DuplicateSkinId(t *testing.T) {
	const csv = `skinId,partType,rarity
dup,Accessory,Common
dup,Top,Rare
`
	if _, err := parseSkinPool(strings.NewReader(csv)); err == nil {
		t.Fatal("expected error for duplicate skinId, got nil")
	}
}

// TestParseSkinPool_SkipsBadRarityAndPart: 미지 rarity/partType 행은 스킵 + 카운트.
func TestParseSkinPool_SkipsBadRarityAndPart(t *testing.T) {
	const csv = `skinId,partType,rarity
ok,Accessory,Common
none_row,Accessory,None
bad_part,Wing,Common
short_row,Common
,Accessory,Common
` // none → BadRarity, Wing → BadPart, short_row(2 cols) → Malformed, empty skinId → Malformed
	// 단, 모든 등급이 채워지지 않으면 5등급 검증에서 에러가 나므로, 채워주는 행 추가.
	full := csv + `u,Accessory,Uncommon
r,Accessory,Rare
q,Accessory,Unique
l,Accessory,Legendary
`
	p, err := parseSkinPool(strings.NewReader(full))
	if err != nil {
		t.Fatalf("parseSkinPool: %v", err)
	}
	st := p.Stats()
	if st.SkippedBadRarity != 1 {
		t.Errorf("SkippedBadRarity = %d, want 1", st.SkippedBadRarity)
	}
	if st.SkippedBadPart != 1 {
		t.Errorf("SkippedBadPart = %d, want 1", st.SkippedBadPart)
	}
	if st.SkippedMalformed != 2 {
		t.Errorf("SkippedMalformed = %d, want 2 (short row + empty skinId)", st.SkippedMalformed)
	}
	if st.Total != st.Loaded+st.SkippedBadRarity+st.SkippedBadPart+st.SkippedMalformed {
		t.Errorf("invariant violated: %+v", st)
	}
}

// TestParseSkinPool_EmptyRarity_Error: 한 등급이라도 비면 에러.
func TestParseSkinPool_EmptyRarity_Error(t *testing.T) {
	const csv = `skinId,partType,rarity
a,Accessory,Common
b,Accessory,Uncommon
c,Accessory,Rare
d,Accessory,Unique
` // Legendary 없음
	if _, err := parseSkinPool(strings.NewReader(csv)); err == nil {
		t.Fatal("expected error for missing Legendary rarity, got nil")
	}
}

// TestParseSkinPool_AcceptsBOM: Excel/Sheets 가 UTF-8 로 재추출하면 첫 셀 앞에
// BOM(U+FEFF) 이 붙는다. 그래도 정상 파일로 받아들이는지 가드한다.
func TestParseSkinPool_AcceptsBOM(t *testing.T) {
	const fixture = "\xef\xbb\xbfskinId,partType,rarity\n" +
		"a,Accessory,Common\n" +
		"b,Accessory,Uncommon\n" +
		"c,Accessory,Rare\n" +
		"d,Accessory,Unique\n" +
		"e,Accessory,Legendary\n"
	p, err := parseSkinPool(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("BOM-prefixed header should be tolerated: %v", err)
	}
	if p.Stats().Loaded != 5 {
		t.Errorf("Loaded = %d, want 5", p.Stats().Loaded)
	}
}

// TestParseSkinPool_BadHeader_Error.
func TestParseSkinPool_BadHeader_Error(t *testing.T) {
	const csv = `id,part,rar
a,Accessory,Common
`
	if _, err := parseSkinPool(strings.NewReader(csv)); err == nil {
		t.Fatal("expected error for bad header, got nil")
	}
}

// TestRealSkinsCSV_NoSkips: 실제 마스터 데이터(Data/skins.csv)를 소스 상대경로로 열어
// 스킵이 하나도 없는지(정규화 누락·데이터 드리프트 방지) 검증한다 — 하드 시그널.
func TestRealSkinsCSV_NoSkips(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	csvPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "Data", "skins.csv")

	p, err := LoadSkinPool(csvPath)
	if err != nil {
		t.Fatalf("LoadSkinPool(%q): %v", csvPath, err)
	}
	st := p.Stats()
	if st.SkippedBadRarity != 0 || st.SkippedBadPart != 0 || st.SkippedMalformed != 0 {
		t.Errorf("real skins.csv produced skips (정규화/데이터 드리프트 의심): %+v", st)
	}
	if st.Loaded != st.Total || st.Total == 0 {
		t.Errorf("Loaded/Total = %d/%d (want equal and > 0)", st.Loaded, st.Total)
	}
}
