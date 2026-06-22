package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
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

		// LOG-7: branch the request-line level by status so 5xx errors surface at
		// Error and 4xx at Warn instead of being buried in the Info stream.
		status := c.Writer.Status()
		var event *zerolog.Event
		switch {
		case status >= 500:
			event = log.Error()
		case status >= 400:
			event = log.Warn()
		default:
			event = log.Info()
		}
		event = event.
			Str("request_id", requestID).
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Int("status", status).
			Dur("latency", time.Since(start))

		if playerID, exists := c.Get("player_id"); exists {
			if id, ok := playerID.(string); ok && id != "" {
				event = event.Str("player_id", id)
			}
		}
		// LOG-7: reject_code lets 4xx request lines be aggregated by reason without a
		// separate per-rejection log line (e.g. high-volume rate-limit bot noise).
		// Middleware/handlers set it via c.Set("reject_code", …).
		if code := c.GetString("reject_code"); code != "" {
			event = event.Str("reject_code", code)
		}

		event.Msg("request")
	}
}
