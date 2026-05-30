package gacha

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// knownPartTypes 는 유효한 partType 고정 집합(정규화 소문자).
// 이 외 partType 행은 스킵(LoadStats.SkippedBadPart 로 카운트). 오타가 동일
// 가중치의 유령 버킷을 만들어 확률을 왜곡하는 것을 차단한다.
var knownPartTypes = map[string]struct{}{
	"top": {}, "bottom": {}, "outfit": {}, "accessory": {}, "face": {}, "effect": {},
}

// LoadStats 는 파싱 결과 통계다. Total 은 헤더 제외 데이터 행 수.
// 불변식: Total == Loaded + SkippedBadRarity + SkippedBadPart + SkippedMalformed.
type LoadStats struct {
	Total, Loaded                                      int
	SkippedBadRarity, SkippedBadPart, SkippedMalformed int
}

// SkinPool 은 skins.csv 에서 로드한 인메모리 가챠 풀이다.
// 생성 후 불변 → 락 없이 동시 읽기 안전.
type SkinPool struct {
	// rarity → 해당 rarity 에 스킨이 1개 이상 있는 partType 목록(정렬).
	partsByRarity map[Rarity][]string
	// partType → rarity → skinId 목록.
	byPartRarity map[string]map[Rarity][]string
	stats        LoadStats
}

// Stats 는 로드 시점에 집계한 파싱 통계를 반환한다.
func (p *SkinPool) Stats() LoadStats { return p.stats }

// LoadSkinPool 은 경로의 CSV 를 열어 파싱한다(기동 시 1회).
func LoadSkinPool(path string) (*SkinPool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open skins csv %q: %w", path, err)
	}
	defer f.Close()
	return parseSkinPool(f)
}

// parseSkinPool 은 io.Reader 에서 풀을 파싱한다(테스트는 strings.NewReader 사용 — cwd 비의존).
func parseSkinPool(r io.Reader) (*SkinPool, error) {
	cr := csv.NewReader(r)
	// 컬럼 수는 수동 검증하여 불일치 행을 스킵(카운트)한다. 기본값(첫 행 기준 강제)
	// 이면 reader 가 불일치 행에서 에러로 중단해 SkippedMalformed 집계가 불가능.
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	// Excel/Sheets 가 UTF-8 로 재추출하면 첫 셀 앞에 BOM(U+FEFF) 이 붙는다.
	// 정상 파일을 부팅 시 거부하지 않도록 첫 셀에서만 BOM 을 떼고 검증한다.
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\xef\xbb\xbf")
	}
	if len(header) != 3 ||
		strings.TrimSpace(header[0]) != "skinId" ||
		strings.TrimSpace(header[1]) != "partType" ||
		strings.TrimSpace(header[2]) != "rarity" {
		return nil, fmt.Errorf("unexpected header %v (want skinId,partType,rarity)", header)
	}

	pool := &SkinPool{
		partsByRarity: map[Rarity][]string{},
		byPartRarity:  map[string]map[Rarity][]string{},
	}
	seen := map[string]struct{}{}

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row %d: %w", pool.stats.Total+1, err)
		}
		pool.stats.Total++

		if len(rec) != 3 {
			pool.stats.SkippedMalformed++
			continue
		}

		skinID := strings.TrimSpace(rec[0])
		partType := strings.ToLower(strings.TrimSpace(rec[1]))
		rarity := Rarity(strings.ToLower(strings.TrimSpace(rec[2])))

		if skinID == "" {
			pool.stats.SkippedMalformed++
			continue
		}
		if !isGachaRarity(rarity) {
			pool.stats.SkippedBadRarity++
			continue
		}
		if _, ok := knownPartTypes[partType]; !ok {
			pool.stats.SkippedBadPart++
			continue
		}
		if _, dup := seen[skinID]; dup {
			return nil, fmt.Errorf("duplicate skinId %q", skinID)
		}
		seen[skinID] = struct{}{}

		if pool.byPartRarity[partType] == nil {
			pool.byPartRarity[partType] = map[Rarity][]string{}
		}
		pool.byPartRarity[partType][rarity] = append(pool.byPartRarity[partType][rarity], skinID)
		pool.stats.Loaded++
	}

	// partsByRarity 구성(정렬: 맵 순회 순서 비의존 → 롤 재현성).
	for partType, byRarity := range pool.byPartRarity {
		for rarity := range byRarity {
			pool.partsByRarity[rarity] = append(pool.partsByRarity[rarity], partType)
		}
	}
	for rarity := range pool.partsByRarity {
		sort.Strings(pool.partsByRarity[rarity])
	}

	// 5등급 모두 채워져야 한다(확률표가 5등급을 하드코딩 → 못 채우면 드롭률 보장 불가).
	for _, rarity := range gachaRarities {
		if len(pool.partsByRarity[rarity]) == 0 {
			return nil, fmt.Errorf("rarity %q has no skins in pool", rarity)
		}
	}

	return pool, nil
}

// Roll 은 한 번의 가챠 추첨을 수행한다:
// rarity(가중) → 해당 rarity 에 존재하는 partType(균등) → 버킷 내 스킨(균등).
func (p *SkinPool) Roll() (Result, error) {
	seed := make([]byte, 16) // 8 rarity, 4 partType, 4 skin
	if _, err := rand.Read(seed); err != nil {
		return Result{}, fmt.Errorf("gacha roll: %w", err)
	}
	hash := sha256.Sum256(seed)

	roll := uint32(binary.BigEndian.Uint64(seed[:8]) % 10000)
	rarity := determineRarity(roll)

	parts := p.partsByRarity[rarity]
	partType := parts[binary.BigEndian.Uint32(seed[8:12])%uint32(len(parts))]

	skins := p.byPartRarity[partType][rarity]
	item := skins[binary.BigEndian.Uint32(seed[12:16])%uint32(len(skins))]

	return Result{
		ItemID:   item,
		Rarity:   rarity,
		SeedHash: hex.EncodeToString(hash[:]),
	}, nil
}
