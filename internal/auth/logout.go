package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// AuthLogout handles DELETE /api/v1/auth/logout.
func (h *Handler) AuthLogout(c *gin.Context) {
	playerID := c.GetString("player_id")
	jti := c.GetString("jti")

	ctx := c.Request.Context()
	if err := logoutSession(ctx, h.kv, playerID, jti); err != nil {
		log.Error().Err(err).Msg("logout: logoutSession")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	c.Status(http.StatusNoContent)
}
