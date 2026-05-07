package middleware

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/gensdeis/stone-server/internal/auth"
	"github.com/gensdeis/stone-server/pkg/kvstore"
)

// JWTAuth validates the Bearer token and checks the jti session in the KV store.
// On success it injects "player_id" and "jti" into the Gin context.
func JWTAuth(pubKey *rsa.PublicKey, kv kvstore.KVStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, ok := bearerToken(c)
		if !ok {
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
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": "session revoked or expired",
					"code":  "UNAUTHORIZED",
				})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "internal error",
				"code":  "INTERNAL_ERROR",
			})
			return
		}

		c.Set("player_id", claims.PlayerID)
		c.Set("jti", claims.ID)
		c.Next()
	}
}

// JWTSignatureAuth validates the JWT signature and expiry but does NOT check
// the session store. Used for logout where the session may already be revoked.
func JWTSignatureAuth(pubKey *rsa.PublicKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, ok := bearerToken(c)
		if !ok {
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
