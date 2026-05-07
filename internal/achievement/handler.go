package achievement

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

type achievementHandlerDB interface {
	QueryRow(ctx context.Context, query string, args ...any) store.Row
	Query(ctx context.Context, query string, args ...any) (store.Rows, error)
	Exec(ctx context.Context, query string, args ...any) (store.Result, error)
}

// allAchievements defines the canonical order for the list response.
var allAchievements = []string{
	AchFirstPull, AchRareUnlock, AchLegendary,
	AchStreak7, AchStreak30, AchCollector,
}

type achievementItem struct {
	AchievementID string     `json:"achievement_id"`
	Unlocked      bool       `json:"unlocked"`
	UnlockedAt    *time.Time `json:"unlocked_at"`
	SteamSynced   bool       `json:"steam_synced"`
}

type Handler struct {
	db    achievementHandlerDB
	kv    kvstore.KVStore
	steam SteamAchievementClient
}

func NewHandler(db store.DB, kv kvstore.KVStore, steam SteamAchievementClient) *Handler {
	return &Handler{db: db, kv: kv, steam: steam}
}

type unlockRequest struct {
	AchievementID string `json:"achievement_id" binding:"required"`
}

type unlockResponse struct {
	Ok              bool       `json:"ok"`
	AlreadyUnlocked bool       `json:"already_unlocked,omitempty"`
	UnlockedAt      *time.Time `json:"unlocked_at,omitempty"`
	SteamSynced     bool       `json:"steam_synced"`
}

// Unlock handles POST /api/v1/achievement/unlock.
func (h *Handler) Unlock(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	var req unlockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing achievement_id", "code": "INVALID_REQUEST"})
		return
	}

	// 1. Idempotency: already unlocked?
	var existingAt time.Time
	var existingSynced bool
	err := h.db.QueryRow(ctx,
		"SELECT unlocked_at, steam_synced FROM player_achievements WHERE player_id = ? AND achievement_id = ?",
		playerID, req.AchievementID,
	).Scan(store.ScanTime(&existingAt), &existingSynced)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Error().Err(err).Msg("achievement unlock: check existing")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	if err == nil {
		c.JSON(http.StatusOK, unlockResponse{
			Ok:              true,
			AlreadyUnlocked: true,
			UnlockedAt:      &existingAt,
			SteamSynced:     existingSynced,
		})
		return
	}

	// 2. Condition check
	if err := Check(ctx, h.db, playerID, req.AchievementID); err != nil {
		if errors.Is(err, ErrConditionNotMet) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "condition not met", "code": "CONDITION_NOT_MET"})
			return
		}
		log.Error().Err(err).Msg("achievement unlock: condition check")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	// 3. Fetch steam_id
	var steamID string
	if err := h.db.QueryRow(ctx,
		"SELECT steam_id FROM players WHERE id = ?", playerID,
	).Scan(&steamID); err != nil {
		log.Error().Err(err).Msg("achievement unlock: fetch steam_id")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	// 4. DB INSERT with ON CONFLICT — protects against the SELECT-then-INSERT race
	// where two concurrent unlock requests both pass the existing-row check.
	now := time.Now().UTC().Truncate(time.Second)
	res, err := h.db.Exec(ctx,
		`INSERT INTO player_achievements (player_id, achievement_id, unlocked_at, steam_synced)
		 VALUES (?, ?, ?, 0)
		 ON CONFLICT (player_id, achievement_id) DO NOTHING`,
		playerID, req.AchievementID, now.Format(time.RFC3339),
	)
	if err != nil {
		log.Error().Err(err).Msg("achievement unlock: insert")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// Race: another request inserted concurrently. Refetch and respond as already_unlocked.
		var raceAt time.Time
		var raceSynced bool
		if err := h.db.QueryRow(ctx,
			"SELECT unlocked_at, steam_synced FROM player_achievements WHERE player_id = ? AND achievement_id = ?",
			playerID, req.AchievementID,
		).Scan(store.ScanTime(&raceAt), &raceSynced); err != nil {
			log.Error().Err(err).Msg("achievement unlock: refetch after conflict")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}
		c.JSON(http.StatusOK, unlockResponse{
			Ok:              true,
			AlreadyUnlocked: true,
			UnlockedAt:      &raceAt,
			SteamSynced:     raceSynced,
		})
		return
	}

	// 5. Steam API sync
	synced := false
	if err := h.steam.SetAchievement(ctx, steamID, req.AchievementID); err != nil {
		log.Warn().Err(err).
			Str("player_id", playerID).
			Str("achievement_id", req.AchievementID).
			Msg("steam achievement sync failed, queuing retry")
		h.enqueueRetry(ctx, playerID, req.AchievementID, err.Error())
	} else {
		if _, err := h.db.Exec(ctx,
			"UPDATE player_achievements SET steam_synced = 1 WHERE player_id = ? AND achievement_id = ?",
			playerID, req.AchievementID,
		); err != nil {
			log.Error().Err(err).Msg("achievement unlock: update steam_synced failed, queuing retry")
			h.enqueueRetry(ctx, playerID, req.AchievementID, err.Error())
		} else {
			synced = true
		}
	}

	c.JSON(http.StatusOK, unlockResponse{Ok: true, UnlockedAt: &now, SteamSynced: synced})
}

// List handles GET /api/v1/achievement/list.
func (h *Handler) List(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	rows, err := h.db.Query(ctx,
		"SELECT achievement_id, unlocked_at, steam_synced FROM player_achievements WHERE player_id = ?",
		playerID,
	)
	if err != nil {
		log.Error().Err(err).Msg("achievement list: query")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	unlocked := make(map[string]achievementItem, len(allAchievements))
	for rows.Next() {
		var item achievementItem
		var unlockedAt time.Time
		if err := rows.Scan(&item.AchievementID, store.ScanTime(&unlockedAt), &item.SteamSynced); err != nil {
			log.Error().Err(err).Msg("achievement list: scan")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}
		item.Unlocked = true
		item.UnlockedAt = &unlockedAt
		unlocked[item.AchievementID] = item
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("achievement list: iterate")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	result := make([]achievementItem, 0, len(allAchievements))
	for _, id := range allAchievements {
		if item, ok := unlocked[id]; ok {
			result = append(result, item)
		} else {
			result = append(result, achievementItem{AchievementID: id})
		}
	}

	c.JSON(http.StatusOK, gin.H{"achievements": result})
}

func (h *Handler) enqueueRetry(ctx context.Context, playerID, achievementID, lastErr string) {
	h.kv.LPush(ctx, "ach:retry", playerID+":"+achievementID)
	if _, err := h.db.Exec(ctx,
		"INSERT INTO achievement_retry_queue (id, player_id, achievement_id, retry_count, last_error) VALUES (?, ?, ?, 0, ?)",
		uuid.New().String(), playerID, achievementID, lastErr,
	); err != nil {
		log.Error().Err(err).Msg("achievement unlock: insert retry queue")
	}
}
