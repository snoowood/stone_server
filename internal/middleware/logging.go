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

		// 요청 단위 바운드 로거를 컨텍스트에 심어, 핸들러가 zerolog.Ctx(c.Request.Context())
		// 로 남기는 로그가 request_id 로 아래 request 라인과 조인되게 한다.
		reqLog := log.With().Str("request_id", requestID).Logger()
		c.Request = c.Request.WithContext(reqLog.WithContext(c.Request.Context()))

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
