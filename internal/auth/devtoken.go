package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DevToken handles GET /api/v1/internal/dev-token?steam_id=X.
// Only registered when APP_ENV == development; used by load-test setup to
// mint JWTs without hitting the auth/steam rate limit.
func (h *Handler) DevToken(c *gin.Context) {
	steamID := c.Query("steam_id")
	if steamID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "steam_id is required"})
		return
	}

	ctx := c.Request.Context()

	playerID, err := upsertPlayer(ctx, h.db, steamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if err := initPlayerState(ctx, h.db, h.cairnCfg, playerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	jti := uuid.New().String()
	tokenStr, expiresAt, err := NewToken(h.privKey, playerID, jti)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if err := enforceSingleSession(ctx, h.kv, playerID, jti); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// ECON-3: re-anchor the accrual clock after the session is established.
	if err := resetAccrualAnchor(ctx, h.db, playerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	refreshToken := uuid.New().String()
	if err := storeRefreshToken(ctx, h.kv, playerID, refreshToken); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, authResponse{
		JWT:          tokenStr,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
	})
}
