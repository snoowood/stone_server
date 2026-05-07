package player

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

const pointsPerClick = 1.0

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

type stateResponse struct {
	PlayerID         string          `json:"player_id"`
	EnlightenmentPts float64         `json:"enlightenment_pts"`
	TimeStoneCnt     int             `json:"time_stone_count"`
	StreakDays       int             `json:"streak_days"`
	NextGachaAt      *time.Time      `json:"next_gacha_at"`
	Inventory        []inventoryItem `json:"inventory"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type clicksRequest struct {
	BatchID string `json:"batch_id" binding:"required"`
	Count   int    `json:"count"`
}

type clicksResponse struct {
	EnlightenmentPts float64 `json:"enlightenment_pts"`
}

// PostClicks handles POST /api/v1/player/clicks.
func (h *Handler) PostClicks(c *gin.Context) {
	var req clicksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "batch_id and count are required", "code": "INVALID_REQUEST"})
		return
	}
	if req.Count <= 0 || req.Count > 300 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "count must be between 1 and 300", "code": "INVALID_COUNT"})
		return
	}

	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	if batchExists(ctx, h.kv, playerID, req.BatchID) {
		c.JSON(http.StatusConflict, gin.H{"error": "duplicate batch_id", "code": "DUPLICATE_BATCH"})
		return
	}
	if !checkTooFrequent(ctx, h.kv, playerID) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "requests too frequent", "code": "TOO_FREQUENT"})
		return
	}
	if !checkHourlyRate(ctx, h.kv, playerID, req.Count) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "hourly click limit exceeded", "code": "RATE_EXCEEDED"})
		return
	}
	if !registerBatch(ctx, h.kv, playerID, req.BatchID) {
		c.JSON(http.StatusConflict, gin.H{"error": "duplicate batch_id", "code": "DUPLICATE_BATCH"})
		return
	}

	delta := float64(req.Count) * pointsPerClick

	var newPts float64
	err := h.db.QueryRow(ctx, `
		UPDATE player_states
		SET enlightenment_pts = enlightenment_pts + ?,
		    updated_at        = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		WHERE player_id = ?
		RETURNING enlightenment_pts
	`, delta, playerID).Scan(&newPts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "player state not found", "code": "NOT_FOUND"})
			return
		}
		log.Error().Err(err).Msg("player clicks: update enlightenment_pts")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	incrHourly(ctx, h.kv, playerID, req.Count)
	c.JSON(http.StatusOK, clicksResponse{EnlightenmentPts: newPts})
}

// GetState handles GET /api/v1/player/state.
func (h *Handler) GetState(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	var resp stateResponse
	resp.PlayerID = playerID

	err := h.db.QueryRow(ctx, `
		SELECT enlightenment_pts, time_stone_count, streak_days, next_gacha_at, updated_at
		FROM player_states
		WHERE player_id = ?
	`, playerID).Scan(
		&resp.EnlightenmentPts,
		&resp.TimeStoneCnt,
		&resp.StreakDays,
		store.ScanNullTime(&resp.NextGachaAt),
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

	c.JSON(http.StatusOK, resp)
}
