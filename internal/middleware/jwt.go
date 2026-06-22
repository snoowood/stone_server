package middleware

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"

	"github.com/gensdeis/stone-server/internal/auth"
	"github.com/gensdeis/stone-server/pkg/kvstore"
)

// JWTAuth validates the Bearer token and checks the jti session in the KV store.
// On success it injects "player_id" and "jti" into the Gin context.
func JWTAuth(pubKey *rsa.PublicKey, kv kvstore.KVStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, ok := bearerToken(c)
		if !ok {
			zerolog.Ctx(c.Request.Context()).Debug().Str("reason", "missing_or_malformed_auth_header").Msg("auth rejected")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing or malformed authorization header",
				"code":  "UNAUTHORIZED",
			})
			return
		}

		claims := &auth.Claims{}
		_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return pubKey, nil
		})
		if err != nil {
			zerolog.Ctx(c.Request.Context()).Debug().Err(err).Str("reason", "invalid_token").Msg("auth rejected")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid or expired token",
				"code":  "UNAUTHORIZED",
			})
			return
		}

		// Verify the session is still live.
		_, err = kv.Get(c.Request.Context(), fmt.Sprintf("session:%s", claims.ID))
		if err != nil {
			if errors.Is(err, kvstore.ErrNotFound) {
				zerolog.Ctx(c.Request.Context()).Warn().Str("reason", "session_revoked").Str("player_id", claims.PlayerID).Msg("auth rejected")
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": "session revoked or expired",
					"code":  "UNAUTHORIZED",
				})
				return
			}
			// Session-store (Redis in prod) is unreachable — this is a transient
			// infra failure, not an auth decision. Return 503 + Retry-After so a
			// store outage doesn't surface as a 500 to every valid-token user and
			// the client paces retries instead of hammering. The ErrNotFound→401
			// revocation path above is unchanged (no security regression).
			zerolog.Ctx(c.Request.Context()).Error().Err(err).Str("reason", "session_store_error").Msg("auth session-store lookup failed")
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "service temporarily unavailable",
				"code":  "SERVICE_UNAVAILABLE",
			})
			return
		}

		c.Set("player_id", claims.PlayerID)
		c.Set("jti", claims.ID)
		// 인증 성공 후 바운드 로거에 player_id 추가 → 이후 핸들러 로그가 player_id 로도 조인된다.
		reqLog := zerolog.Ctx(c.Request.Context()).With().Str("player_id", claims.PlayerID).Logger()
		c.Request = c.Request.WithContext(reqLog.WithContext(c.Request.Context()))
		c.Next()
	}
}

// JWTSignatureAuth validates the JWT signature and expiry but does NOT check
// the session store. Used for logout where the session may already be revoked.
func JWTSignatureAuth(pubKey *rsa.PublicKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, ok := bearerToken(c)
		if !ok {
			zerolog.Ctx(c.Request.Context()).Debug().Str("reason", "missing_or_malformed_auth_header").Msg("auth rejected")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing or malformed authorization header",
				"code":  "UNAUTHORIZED",
			})
			return
		}

		claims := &auth.Claims{}
		_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return pubKey, nil
		})
		if err != nil {
			zerolog.Ctx(c.Request.Context()).Debug().Err(err).Str("reason", "invalid_token").Msg("auth rejected")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid or expired token",
				"code":  "UNAUTHORIZED",
			})
			return
		}

		c.Set("player_id", claims.PlayerID)
		c.Set("jti", claims.ID)
		c.Next()
	}
}

func bearerToken(c *gin.Context) (string, bool) {
	h := c.GetHeader("Authorization")
	if h == "" {
		return "", false
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
