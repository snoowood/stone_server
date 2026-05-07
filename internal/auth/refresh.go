package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/gensdeis/stone-server/pkg/kvstore"
)

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// refreshResponse rotates the refresh_token: clients MUST persist the new token
// and discard the one they sent — the old one is invalidated server-side.
type refreshResponse struct {
	JWT          string    `json:"jwt"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// AuthRefresh handles POST /api/v1/auth/refresh.
func (h *Handler) AuthRefresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh_token is required", "code": "INVALID_REQUEST"})
		return
	}

	ctx := c.Request.Context()

	playerID, err := lookupRefreshToken(ctx, h.kv, req.RefreshToken)
	if err != nil {
		if errors.Is(err, kvstore.ErrNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token", "code": "INVALID_REFRESH_TOKEN"})
			return
		}
		log.Error().Err(err).Msg("refresh: lookup refresh token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	acquired, err := acquireRefreshLock(ctx, h.kv, playerID)
	if err != nil {
		log.Error().Err(err).Msg("refresh: acquire lock")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	if !acquired {
		c.JSON(http.StatusConflict, gin.H{"error": "refresh already in progress", "code": "REFRESH_IN_PROGRESS"})
		return
	}
	defer releaseRefreshLock(h.kv, playerID)

	cooling, err := isRefreshCoolingDown(ctx, h.kv, playerID)
	if err != nil {
		log.Error().Err(err).Msg("refresh: check cooldown")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	if cooling {
		c.JSON(http.StatusConflict, gin.H{"error": "refresh already in progress", "code": "REFRESH_IN_PROGRESS"})
		return
	}

	storedToken, err := getStoredRefreshToken(ctx, h.kv, playerID)
	if err != nil {
		if errors.Is(err, kvstore.ErrNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token", "code": "INVALID_REFRESH_TOKEN"})
			return
		}
		log.Error().Err(err).Msg("refresh: get stored refresh token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	if storedToken != req.RefreshToken {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token revoked by new login", "code": "REFRESH_TOKEN_REVOKED"})
		return
	}

	newJTI := uuid.New().String()
	tokenStr, expiresAt, err := NewToken(h.privKey, playerID, newJTI)
	if err != nil {
		log.Error().Err(err).Msg("refresh: generate jwt")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	if err := enforceSingleSession(ctx, h.kv, playerID, newJTI); err != nil {
		if errors.Is(err, ErrSessionInFlight) {
			c.JSON(http.StatusConflict, gin.H{"error": "concurrent session in progress", "code": "LOGIN_IN_PROGRESS"})
			return
		}
		log.Error().Err(err).Msg("refresh: enforce single session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	if err := setRefreshCooldown(ctx, h.kv, playerID); err != nil {
		log.Warn().Err(err).Msg("refresh: set cooldown failed (non-fatal)")
	}

	newRefreshToken := uuid.New().String()
	if err := storeRefreshToken(ctx, h.kv, playerID, newRefreshToken); err != nil {
		log.Error().Err(err).Msg("refresh: store rotated refresh token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	// Invalidate the lookup for the old token so it cannot be replayed.
	invalidateRefreshLookup(ctx, h.kv, req.RefreshToken)

	c.JSON(http.StatusOK, refreshResponse{
		JWT:          tokenStr,
		RefreshToken: newRefreshToken,
		ExpiresAt:    expiresAt,
	})
}
