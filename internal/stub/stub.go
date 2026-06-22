package stub

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func NoOp(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "stub"})
}

// PlayerIDFromHeader sets player_id in context from X-Player-ID header.
// Used only when APP_ENV == "development" to verify player_id-based rate limiting.
func PlayerIDFromHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		if id := c.GetHeader("X-Player-ID"); id != "" {
			c.Set("player_id", id)
		}
		c.Next()
	}
}
