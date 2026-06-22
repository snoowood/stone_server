package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"github.com/gensdeis/stone-server/pkg/kvstore"
)

type rlPolicy struct {
	limit   int
	ipBased bool
}

// 03_api.md Rate Limit 정책
var rlPolicies = map[string]rlPolicy{
	"POST:/api/v1/auth/steam":         {limit: 10, ipBased: true},
	"POST:/api/v1/gacha/pull":         {limit: 5, ipBased: false},
	"POST:/api/v1/player/sync":        {limit: 4, ipBased: false},
	"POST:/api/v1/achievement/unlock": {limit: 20, ipBased: false},
}

var defaultPolicy = rlPolicy{limit: 60, ipBased: false}

func RateLimiter(kv kvstore.KVStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		fullPath := c.FullPath()
		if fullPath == "" {
			c.Next()
			return
		}

		policyKey := c.Request.Method + ":" + fullPath
		policy, ok := rlPolicies[policyKey]
		if !ok {
			policy = defaultPolicy
		}

		identifier := resolveIdentifier(c, policy.ipBased)
		key := fmt.Sprintf("ratelimit:%s:%s:%s", identifier, c.Request.Method, fullPath)

		count, err := incrRateLimit(c.Request.Context(), kv, key)
		if err != nil {
			// Store failure → fail open. 침묵 시 열화된 리미터를 탐지 못 하므로 Error 로 남긴다.
			zerolog.Ctx(c.Request.Context()).Error().Err(err).
				Str("path", fullPath).Msg("rate limiter fail-open: kv store error")
			c.Next()
			return
		}

		if int(count) > policy.limit {
			// 정상 사용자 거절 → Debug(요청 라인이 이미 429 status 를 남김). 인프라 실패만 Error.
			zerolog.Ctx(c.Request.Context()).Debug().
				Str("identifier", identifier).Str("path", fullPath).
				Int("limit", policy.limit).Int64("count", count).Msg("rate limit exceeded")
			// LOG-7: tag the request line so rate-limit rejections aggregate via reject_code
			// rather than a separate Info line per (potentially bot-driven) request.
			c.Set("reject_code", "RATE_LIMIT_EXCEEDED")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
				"code":  "RATE_LIMIT_EXCEEDED",
			})
			return
		}

		c.Next()
	}
}

// incrRateLimit increments the counter and sets a 60s TTL on first increment.
func incrRateLimit(ctx context.Context, kv kvstore.KVStore, key string) (int64, error) {
	count, err := kv.IncrBy(ctx, key, 1)
	if err != nil {
		return 0, err
	}
	if count == 1 {
		if err := kv.Expire(ctx, key, 60*time.Second); err != nil {
			// TTL 설정 실패 시 TTL 없는 카운터가 영구히 남아 해당 식별자가 무기한 429 로
			// 묶일 수 있다(조용한 self-DoS). best-effort 로 키를 지워 다음 요청이 새로
			// 시작하게 하고, 실패를 로그로 남겨 관측 가능하게 한다.
			zerolog.Ctx(ctx).Error().Err(err).Str("key", key).Msg("rate limiter: failed to set TTL (counter dropped)")
			if delErr := kv.Del(ctx, key); delErr != nil {
				// Del 까지 실패하면 TTL 없는 키가 남는다 — 마지막 침묵 경로를 닫는다.
				zerolog.Ctx(ctx).Error().Err(delErr).Str("key", key).Msg("rate limiter: failed to delete TTL-less key")
			}
		}
	}
	return count, nil
}

func resolveIdentifier(c *gin.Context, ipBased bool) string {
	if !ipBased {
		if playerID, exists := c.Get("player_id"); exists {
			if id, ok := playerID.(string); ok && id != "" {
				return id
			}
		}
	}
	return c.ClientIP()
}
