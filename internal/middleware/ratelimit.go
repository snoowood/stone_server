package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

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
	"POST:/api/v1/player/clicks":      {limit: 10, ipBased: false},
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
			// Store failure → fail open
			c.Next()
			return
		}

		if int(count) > policy.limit {
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
		kv.Expire(ctx, key, 60*time.Second)
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
