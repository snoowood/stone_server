package player

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/gensdeis/stone-server/internal/cairn"
	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

type Handler struct {
	db store.DB
	kv kvstore.KVStore
}

func NewHandler(db store.DB, kv kvstore.KVStore) *Handler {
	return &Handler{db: db, kv: kv}
}

type inventoryItem struct {
	ItemID     string    `json:"item_id"`
	Rarity     string    `json:"rarity"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// cairnStateResponse mirrors the server-authoritative WishCairn state.
// SlotCount / MaxLayers / SpawnIntervalSec 는 클라가 표시 외삽에 쓰는 게임 상수.
// Slots 는 매 read 시 (now - started_at) / interval 로 derive 된 슬롯 배열.
type cairnStateResponse struct {
	SlotCount        int               `json:"slot_count"`
	MaxLayers        int               `json:"max_layers"`
	SpawnIntervalSec int               `json:"spawn_interval_sec"`
	Slots            []cairn.SlotState `json:"slots"`
}

type stateResponse struct {
	PlayerID          string             `json:"player_id"`
	EnlightenmentPts  float64            `json:"enlightenment_pts"`
	EnlightenmentRate float64            `json:"enlightenment_rate"`
	TimeStoneCnt      int                `json:"time_stone_count"`
	StreakDays        int                `json:"streak_days"`
	NextGachaAt       *time.Time         `json:"next_gacha_at"`
	LastSyncAt        *time.Time         `json:"last_sync_at"`
	Inventory         []inventoryItem    `json:"inventory"`
	Cairn             cairnStateResponse `json:"cairn"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

type syncResponse struct {
	EnlightenmentPts float64    `json:"enlightenment_pts"`
	LastSyncAt       *time.Time `json:"last_sync_at"`
}

// Sync handles POST /api/v1/player/sync.
// It computes enlightenment earned since last_sync_at using the player's
// current enlightenment_rate and credits it, then updates last_sync_at = now.
func (h *Handler) Sync(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	var lastSyncAt *time.Time
	var rate float64
	err := h.db.QueryRow(ctx, `
		SELECT enlightenment_rate, last_sync_at
		FROM player_states
		WHERE player_id = ?
	`, playerID).Scan(&rate, store.ScanNullTime(&lastSyncAt))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "player state not found", "code": "NOT_FOUND"})
			return
		}
		log.Error().Err(err).Msg("player sync: query player_states")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	now := time.Now().UTC()
	var delta float64
	if lastSyncAt != nil {
		elapsed := now.Sub(*lastSyncAt).Seconds()
		if elapsed > 0 {
			delta = elapsed * rate
		}
	}

	var newPts float64
	var newSyncAt *time.Time
	err = h.db.QueryRow(ctx, `
		UPDATE player_states
		SET enlightenment_pts = enlightenment_pts + ?,
		    last_sync_at      = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
		    updated_at        = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		WHERE player_id = ?
		RETURNING enlightenment_pts, last_sync_at
	`, delta, playerID).Scan(&newPts, store.ScanNullTime(&newSyncAt))
	if err != nil {
		log.Error().Err(err).Msg("player sync: update enlightenment_pts")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, syncResponse{EnlightenmentPts: newPts, LastSyncAt: newSyncAt})
}

// GetState handles GET /api/v1/player/state.
func (h *Handler) GetState(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	var resp stateResponse
	resp.PlayerID = playerID

	err := h.db.QueryRow(ctx, `
		SELECT enlightenment_pts, enlightenment_rate, time_stone_count, streak_days,
		       next_gacha_at, last_sync_at, updated_at
		FROM player_states
		WHERE player_id = ?
	`, playerID).Scan(
		&resp.EnlightenmentPts,
		&resp.EnlightenmentRate,
		&resp.TimeStoneCnt,
		&resp.StreakDays,
		store.ScanNullTime(&resp.NextGachaAt),
		store.ScanNullTime(&resp.LastSyncAt),
		store.ScanTime(&resp.UpdatedAt),
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "player state not found", "code": "NOT_FOUND"})
			return
		}
		log.Error().Err(err).Msg("player state: query player_states")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	rows, err := h.db.Query(ctx, `
		SELECT item_id, rarity, acquired_at
		FROM inventories
		WHERE player_id = ?
		ORDER BY acquired_at
	`, playerID)
	if err != nil {
		log.Error().Err(err).Msg("player state: query inventories")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	resp.Inventory = []inventoryItem{}
	for rows.Next() {
		var item inventoryItem
		if err := rows.Scan(&item.ItemID, &item.Rarity, store.ScanTime(&item.AcquiredAt)); err != nil {
			log.Error().Err(err).Msg("player state: scan inventory row")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}
		resp.Inventory = append(resp.Inventory, item)
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("player state: iterate inventories")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	// M2: WishCairn 슬롯 상태 — 매 read 시 derive.
	slots, err := cairn.LoadSlots(ctx, h.db, playerID, time.Now())
	if err != nil {
		log.Error().Err(err).Msg("player state: load cairn slots")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	resp.Cairn = cairnStateResponse{
		SlotCount:        cairn.SlotCount,
		MaxLayers:        cairn.MaxLayers,
		SpawnIntervalSec: cairn.SpawnIntervalSeconds,
		Slots:            slots,
	}

	c.JSON(http.StatusOK, resp)
}
