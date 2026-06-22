package auth

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type devAuthRequest struct {
	UUID string `json:"uuid" binding:"required"`
}

// AuthDev handles POST /api/v1/auth/dev — UUID-based passwordless login.
// Registered only when APP_ENV == "development".
// The client sends a stable UUID it generated and persisted locally; the server
// upserts a player keyed on that UUID and returns the same JWT/refresh pair as
// the live Steam flow.
func (h *Handler) AuthDev(c *gin.Context) {
	var req devAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "uuid is required", "code": "INVALID_REQUEST"})
		return
	}
	if _, err := uuid.Parse(req.UUID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "uuid must be a valid UUID", "code": "INVALID_REQUEST"})
		return
	}

	ctx := c.Request.Context()

	playerID, err := upsertPlayer(ctx, h.db, req.UUID)
	if err != nil {
		log.Error().Err(err).Str("uuid", req.UUID).Msg("auth/dev: upsert player")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	if err := initPlayerState(ctx, h.db, h.cairnCfg, playerID); err != nil {
		log.Error().Err(err).Str("player_id", playerID).Msg("auth/dev: init player state")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	if err := updateLoginStreak(ctx, h.db, playerID); err != nil {
		log.Error().Err(err).Str("player_id", playerID).Msg("auth/dev: update login streak")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	jti := uuid.New().String()
	tokenStr, expiresAt, err := NewToken(h.privKey, playerID, jti)
	if err != nil {
		log.Error().Err(err).Msg("auth/dev: generate jwt")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	if err := enforceSingleSession(ctx, h.kv, playerID, jti); err != nil {
		if errors.Is(err, ErrSessionInFlight) {
			c.JSON(http.StatusConflict, gin.H{"error": "concurrent login in progress", "code": "LOGIN_IN_PROGRESS"})
			return
		}
		log.Error().Err(err).Msg("auth/dev: enforce single session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	refreshToken := uuid.New().String()
	if err := storeRefreshToken(ctx, h.kv, playerID, refreshToken); err != nil {
		log.Error().Err(err).Msg("auth/dev: store refresh token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, authResponse{
		JWT:          tokenStr,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
	})
}
