// Package vow implements the Vow crafting feature (POST /api/v1/vow/pray):
// 재료 스킨 N개를 소비해 base_rarity 를 산출하고, n_power(소비 깨달음)에 비례한
// 성공률로 target_rarity(=base+1) 스킨 1개를 제작한다. 실패 시 base_rarity 스킨을 받는다.
//
// 클라이언트 계약(권위는 서버):
//
//	request : Assets/Scripts/Network/Dto/VowDto.cs (VowPrayRequestDto)
//	response: VowPrayResponseDto (snake_case)
//
// base/target/성공률 산식은 클라 VowSystem.cs 와 정확히 일치해야 한다.
package vow

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/gensdeis/stone-server/internal/gacha"
	"github.com/gensdeis/stone-server/pkg/gameconfig"
	"github.com/gensdeis/stone-server/pkg/store"
)

// Handler holds the dependencies for the vow endpoint.
type Handler struct {
	db                store.DB
	pool              *gacha.SkinPool
	requiredItemCount int
	tiers             map[string]gameconfig.VowTier // key: lowercased target rarity
	allowDebug        bool                          // APP_ENV == development: honor debug_options
}

// NewHandler wires the vow handler. tiers 는 lowercased TargetRarity 키 맵으로 인덱싱한다.
func NewHandler(db store.DB, pool *gacha.SkinPool, requiredItemCount int, tiers []gameconfig.VowTier, allowDebug bool) *Handler {
	m := make(map[string]gameconfig.VowTier, len(tiers))
	for _, t := range tiers {
		m[t.TargetRarity] = t
	}
	return &Handler{db: db, pool: pool, requiredItemCount: requiredItemCount, tiers: m, allowDebug: allowDebug}
}

type material struct {
	ItemID string `json:"item_id"`
	Count  int    `json:"count"`
}

type debugOptions struct {
	ForceSuccess        bool `json:"force_success"`
	ForceFailure        bool `json:"force_failure"`
	InfiniteNPower      bool `json:"infinite_n_power"`
	IgnoreRequiredItems bool `json:"ignore_required_items"`
	LocalPray           bool `json:"local_pray"`
}

type prayRequest struct {
	Materials    []material    `json:"materials"`
	NPower       float64       `json:"n_power"`
	DebugOptions *debugOptions `json:"debug_options"`
}

// inventoryItem mirrors player.inventoryItem (snake_case) for the response inventory[].
type inventoryItem struct {
	ItemID     string    `json:"item_id"`
	ItemType   string    `json:"item_type"`
	Rarity     string    `json:"rarity"`
	Count      int       `json:"count"`
	SourceType string    `json:"source_type"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// prayResponse maps VowPrayResponseDto 1:1.
type prayResponse struct {
	Result       string            `json:"result"` // "Success" / "Failed" (PascalCase: 클라 VowResult.ToString() 비교)
	RewardItemID string            `json:"reward_item_id"`
	RewardRarity string            `json:"reward_rarity"`
	Reward       gacha.RewardGrant `json:"reward"`
	IsDuplicate  bool              `json:"is_duplicate"`
	NewCount     int               `json:"new_count"`
	SourceType   string            `json:"source_type"`
	BaseRarity   string            `json:"base_rarity"`
	TargetRarity string            `json:"target_rarity"`
	SuccessRate  float64           `json:"success_rate"`
	BalanceAfter float64           `json:"balance_after"`
	LastSyncAt   time.Time         `json:"last_sync_at"`
	Inventory    []inventoryItem   `json:"inventory"`
	IsNewReward  bool              `json:"is_new_reward"`
}

var (
	errInsufficientPoints    = errors.New("insufficient points")
	errInsufficientMaterials = errors.New("insufficient materials")
	errNoRewardPool          = errors.New("no reward pool")
)

// Pray handles POST /api/v1/vow/pray. 검증(권위)은 tx 전에 fail-fast 로 끝낸다.
func (h *Handler) Pray(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	var req prayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body", "code": "INVALID_REQUEST"})
		return
	}

	ignoreItems := h.allowDebug && req.DebugOptions != nil && req.DebugOptions.IgnoreRequiredItems

	total := 0
	for _, m := range req.Materials {
		if m.Count <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "material count must be positive", "code": "INVALID_REQUEST"})
			return
		}
		total += m.Count
	}
	if !ignoreItems && total != h.requiredItemCount {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("vow requires exactly %d items", h.requiredItemCount),
			"code":  "INVALID_REQUEST",
		})
		return
	}

	// 서버 권위: 재료 등급으로 base/target 을 직접 산출(클라 계산값 불신).
	baseRarity, targetRarity, ok := h.computeRarities(req.Materials)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "materials do not produce a craftable rarity", "code": "INVALID_REQUEST"})
		return
	}

	tier, ok := h.tiers[string(targetRarity)]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no vow config for target rarity", "code": "INVALID_REQUEST"})
		return
	}

	infinite := h.allowDebug && req.DebugOptions != nil && req.DebugOptions.InfiniteNPower
	if !infinite {
		if req.NPower < tier.MinNPower || req.NPower > tier.MaxNPower {
			c.JSON(http.StatusBadRequest, gin.H{"error": "n_power out of range", "code": "INVALID_REQUEST"})
			return
		}
	}
	successRate := calcSuccessRate(tier, req.NPower)

	resp, err := h.execPray(ctx, playerID, req, baseRarity, targetRarity, successRate, infinite, ignoreItems)
	if err != nil {
		lg := zerolog.Ctx(ctx)
		switch {
		case errors.Is(err, errInsufficientPoints):
			lg.Debug().Str("code", "INSUFFICIENT_POINTS").Msg("vow rejected")
			c.JSON(http.StatusConflict, gin.H{"error": "insufficient enlightenment points", "code": "INSUFFICIENT_POINTS"})
		case errors.Is(err, errInsufficientMaterials):
			lg.Debug().Str("code", "INSUFFICIENT_MATERIALS").Msg("vow rejected")
			c.JSON(http.StatusConflict, gin.H{"error": "insufficient materials", "code": "INSUFFICIENT_MATERIALS"})
		case errors.Is(err, errNoRewardPool):
			lg.Error().Msg("vow: no reward pool for result rarity")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		default:
			lg.Error().Err(err).Msg("vow pray")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		}
		return
	}
	c.JSON(http.StatusOK, resp)
}

// computeRarities mirrors VowSystem.TryComputeBaseAndTargetRarity:
// 각 재료 등급은 Common..Unique(>None && <Legendary)여야 하고,
// average = Σ(order×count)/Σcount, rounded = floor(avg+0.5), base=rounded(Common..<Legendary),
// target = base+1.
func (h *Handler) computeRarities(materials []material) (base, target gacha.Rarity, ok bool) {
	total := 0
	sum := 0
	for _, m := range materials {
		r, found := h.pool.RarityOf(m.ItemID)
		if !found {
			return "", "", false
		}
		o := rarityOrder(r)
		if o <= 0 || o >= 5 { // > None && < Legendary
			return "", "", false
		}
		total += m.Count
		sum += o * m.Count
	}
	if total <= 0 {
		return "", "", false
	}
	avg := float64(sum) / float64(total)
	rounded := int(math.Floor(avg + 0.5))
	if rounded < 1 || rounded >= 5 { // Common..< Legendary
		return "", "", false
	}
	return orderRarity(rounded), orderRarity(rounded + 1), true
}

func (h *Handler) execPray(
	ctx context.Context,
	playerID string,
	req prayRequest,
	baseRarity, targetRarity gacha.Rarity,
	successRate float64,
	infinite, ignoreItems bool,
) (*prayResponse, error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 1. Atomic accrue + deduct cost(=n_power) + last_sync_at 갱신 (gacha execPull 패턴).
	//    infinite 디버그면 cost=0 → 차감 없이 accrue 결과만 받는다.
	cost := req.NPower
	if infinite {
		cost = 0
	}
	var newBalance float64
	var lastSyncAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE player_states
		SET enlightenment_pts = enlightenment_pts +
			MAX(0, COALESCE(
				(CAST(strftime('%s','now') AS REAL) - CAST(strftime('%s', last_sync_at) AS REAL)) * enlightenment_rate,
				0
			)) - ?,
		    last_sync_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE player_id = ?
		  AND enlightenment_pts +
		      MAX(0, COALESCE(
		          (CAST(strftime('%s','now') AS REAL) - CAST(strftime('%s', last_sync_at) AS REAL)) * enlightenment_rate,
		          0
		      )) >= ?
		RETURNING enlightenment_pts, last_sync_at
	`, cost, playerID, cost).Scan(&newBalance, store.ScanTime(&lastSyncAt))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInsufficientPoints
	}
	if err != nil {
		return nil, err
	}

	// 2. 재료 소비 — 각 행 count -= n (count >= n 조건). 한 건이라도 미충족이면 rollback.
	if !ignoreItems {
		for _, m := range req.Materials {
			res, err := tx.Exec(ctx,
				"UPDATE inventories SET count = count - ? WHERE player_id = ? AND item_id = ? AND count >= ?",
				m.Count, playerID, m.ItemID, m.Count)
			if err != nil {
				return nil, err
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return nil, errInsufficientMaterials
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM inventories WHERE player_id = ? AND count <= 0", playerID); err != nil {
			return nil, err
		}
	}

	// 3. 성공 판정 (crypto/rand). debug force_*는 allowDebug 일 때만.
	success, err := h.rollSuccess(req.DebugOptions, successRate)
	if err != nil {
		return nil, err
	}
	rewardRarity := baseRarity
	if success {
		rewardRarity = targetRarity
	}

	// 4. 보상 스킨 선택 + 인벤토리 grant(공용 GrantItem 재사용, source_type=vow_reward).
	rewardItemID, ok := h.pool.PickByRarity(rewardRarity)
	if !ok {
		return nil, errNoRewardPool
	}
	grant, err := gacha.GrantItem(ctx, tx, playerID, rewardItemID, string(rewardRarity), gacha.ItemTypeStoneSkin, gacha.SourceVowReward)
	if err != nil {
		return nil, err
	}

	result := "Failed"
	if success {
		result = "Success"
	}

	// 5. vow_logs 감사 행 — gacha_logs(execPull step 7) 와 대칭. 같은 tx 안에서 INSERT 해
	//    포인트 차감·재료 소모·보상 지급과 원자적으로 커밋한다(분쟁/밸런스 증거 유실 방지).
	//    materials 는 정규화 자식 테이블 대신 JSON 한 칼럼(단순성 우선).
	materialsJSON, err := json.Marshal(req.Materials)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO vow_logs
		    (id, player_id, base_rarity, target_rarity, success_rate, cost_points,
		     result, reward_item_id, reward_rarity, is_duplicate, materials)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), playerID, string(baseRarity), string(targetRarity),
		successRate, cost, result, rewardItemID, string(rewardRarity),
		grant.IsDuplicate, string(materialsJSON),
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// LOG-4: one structured economy line per committed vow, emitted right after the
	// commit (before the post-commit inventory reload) so a reload failure can't drop
	// the record. request_id/player_id come from the bound logger.
	zerolog.Ctx(ctx).Info().
		Str("event", "vow_pray").
		Str("result", result).
		Str("base_rarity", string(baseRarity)).
		Str("target_rarity", string(targetRarity)).
		Float64("success_rate", successRate).
		Str("reward_item_id", rewardItemID).
		Str("reward_rarity", string(rewardRarity)).
		Bool("is_duplicate", grant.IsDuplicate).
		Float64("balance_after", newBalance).
		Msg("vow pray committed")

	// 6. 커밋 후 전체 인벤토리 재조회(클라가 권위 상태로 교체).
	inv, err := h.loadInventory(ctx, playerID)
	if err != nil {
		return nil, err
	}

	return &prayResponse{
		Result:       result,
		RewardItemID: rewardItemID,
		RewardRarity: string(rewardRarity),
		Reward:       grant,
		IsDuplicate:  grant.IsDuplicate,
		NewCount:     grant.NewCount,
		SourceType:   gacha.SourceVowReward,
		BaseRarity:   string(baseRarity),
		TargetRarity: string(targetRarity),
		SuccessRate:  successRate,
		BalanceAfter: newBalance,
		LastSyncAt:   lastSyncAt,
		Inventory:    inv,
		IsNewReward:  !grant.IsDuplicate,
	}, nil
}

func (h *Handler) rollSuccess(d *debugOptions, successRate float64) (bool, error) {
	if h.allowDebug && d != nil {
		if d.ForceSuccess {
			return true, nil
		}
		if d.ForceFailure {
			return false, nil
		}
	}
	r, err := rollPercent()
	if err != nil {
		return false, err
	}
	return r <= successRate, nil
}

func (h *Handler) loadInventory(ctx context.Context, playerID string) ([]inventoryItem, error) {
	rows, err := h.db.Query(ctx, `
		SELECT item_id, rarity, count, acquired_at, item_type, source_type
		FROM inventories
		WHERE player_id = ?
		ORDER BY acquired_at
	`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	inv := []inventoryItem{}
	for rows.Next() {
		var it inventoryItem
		if err := rows.Scan(&it.ItemID, &it.Rarity, &it.Count, store.ScanTime(&it.AcquiredAt), &it.ItemType, &it.SourceType); err != nil {
			return nil, err
		}
		inv = append(inv, it)
	}
	return inv, rows.Err()
}

// calcSuccessRate mirrors VowSystem.CalculateSuccessRate (InverseLerp→Lerp→Clamp).
func calcSuccessRate(tier gameconfig.VowTier, nPower float64) float64 {
	if tier.MaxNPower <= tier.MinNPower {
		return clamp(tier.MaxSuccessRate, 0, 100)
	}
	t := (nPower - tier.MinNPower) / (tier.MaxNPower - tier.MinNPower)
	t = clamp(t, 0, 1) // Mathf.InverseLerp 은 [0,1] 로 clamp
	rate := tier.MinSuccessRate + (tier.MaxSuccessRate-tier.MinSuccessRate)*t
	return clamp(rate, 0, 100)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// rarityOrder maps gacha.Rarity → 클라 Rarity enum 서수(None=0,Common=1..Legendary=5).
func rarityOrder(r gacha.Rarity) int {
	switch r {
	case gacha.Common:
		return 1
	case gacha.Uncommon:
		return 2
	case gacha.Rare:
		return 3
	case gacha.Unique:
		return 4
	case gacha.Legendary:
		return 5
	default:
		return 0
	}
}

func orderRarity(o int) gacha.Rarity {
	switch o {
	case 1:
		return gacha.Common
	case 2:
		return gacha.Uncommon
	case 3:
		return gacha.Rare
	case 4:
		return gacha.Unique
	case 5:
		return gacha.Legendary
	default:
		return ""
	}
}

// rollPercent 은 [0,100) 균등 난수를 crypto/rand 로 생성한다.
func rollPercent() (float64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	u := float64(binary.BigEndian.Uint64(b[:])>>11) / float64(uint64(1)<<53)
	return u * 100, nil
}
