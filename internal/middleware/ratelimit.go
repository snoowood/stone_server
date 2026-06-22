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

		count, err := incrRateLimit(c.Request.Context(), kv, key, rateLimitWindow)
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

const rateLimitWindow = 60 * time.Second

// incrRateLimit increments the fixed-window counter for key and returns the new count.
//
// SEC-3: the first hit of a window is written with SetNX, which sets the value AND its
// TTL in one atomic op. The previous IncrBy-then-Expire two-step could leave a TTL-less
// key if the process died (or the Expire failed) in between, pinning that identifier at
// 429 forever (a silent self-DoS). With prod sharing one Redis across instances this is
// a real, not latent, risk.
func incrRateLimit(ctx context.Context, kv kvstore.KVStore, key string, window time.Duration) (int64, error) {
	ok, err := kv.SetNX(ctx, key, "1", window)
	if err != nil {
		return 0, err
	}
	if ok {
		return 1, nil
	}
	count, err := kv.IncrBy(ctx, key, 1)
	if err != nil {
		return 0, err
	}
	if count == 1 {
		// The key expired between SetNX and IncrBy, so IncrBy re-created it without a
		// TTL. Re-arm it; if that fails, drop the key so the next request starts a fresh
		// window instead of being pinned forever on a TTL-less poison key (self-DoS).
		if err := kv.Expire(ctx, key, window); err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Str("key", key).Msg("rate limiter: failed to re-arm TTL, dropping key")
			if delErr := kv.Del(ctx, key); delErr != nil {
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
