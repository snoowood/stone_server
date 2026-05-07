package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		requestID := uuid.New().String()
		c.Set("request_id", requestID)

		c.Next()

		event := log.Info().
			Str("request_id", requestID).
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", c.Writer.Status()).
			Dur("latency", time.Since(start))

		if playerID, exists := c.Get("player_id"); exists {
			if id, ok := playerID.(string); ok && id != "" {
				event = event.Str("player_id", id)
			}
		}

		event.Msg("request")
	}
}
