package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

// Handler handles Steam authentication requests.
type Handler struct {
	db      store.DB
	kv      kvstore.KVStore
	steam   SteamClient
	privKey *rsa.PrivateKey
}

// NewHandler creates a Handler with the given dependencies.
func NewHandler(db store.DB, kv kvstore.KVStore, steam SteamClient, privKey *rsa.PrivateKey) *Handler {
	return &Handler{db: db, kv: kv, steam: steam, privKey: privKey}
}

type authRequest struct {
	Ticket string `json:"ticket" binding:"required"`
}

type authResponse struct {
	JWT          string    `json:"jwt"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// AuthSteam handles POST /api/v1/auth/steam.
func (h *Handler) AuthSteam(c *gin.Context) {
	var req authRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ticket is required", "code": "INVALID_REQUEST"})
		return
	}

	ctx := c.Request.Context()

	hash := ticketHash(req.Ticket)
	claimed, err := claimTicket(ctx, h.kv, hash)
	if err != nil {
		log.Error().Err(err).Msg("auth: claim ticket")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	if !claimed {
		c.JSON(http.StatusConflict, gin.H{"error": "ticket already used", "code": "TICKET_USED"})
		return
	}

	success := false
	defer func() {
		if !success {
			releaseTicket(h.kv, hash)
		}
	}()

	steamID, err := h.steam.AuthenticateTicket(ctx, req.Ticket)
	if err != nil {
		if errors.Is(err, ErrInvalidTicket) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid steam ticket", "code": "INVALID_TICKET"})
			return
		}
		log.Error().Err(err).Msg("auth: steam authenticate ticket")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	playerID, err := upsertPlayer(ctx, h.db, steamID)
	if err != nil {
		log.Error().Err(err).Str("steam_id", steamID).Msg("auth: upsert player")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	if err := initPlayerState(ctx, h.db, playerID); err != nil {
		log.Error().Err(err).Str("player_id", playerID).Msg("auth: init player state")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	if err := updateLoginStreak(ctx, h.db, playerID); err != nil {
		log.Error().Err(err).Str("player_id", playerID).Msg("auth: update login streak")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	jti := uuid.New().String()
	tokenStr, expiresAt, err := NewToken(h.privKey, playerID, jti)
	if err != nil {
		log.Error().Err(err).Msg("auth: generate jwt")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	if err := enforceSingleSession(ctx, h.kv, playerID, jti); err != nil {
		if errors.Is(err, ErrSessionInFlight) {
			c.JSON(http.StatusConflict, gin.H{"error": "concurrent login in progress", "code": "LOGIN_IN_PROGRESS"})
			return
		}
		log.Error().Err(err).Msg("auth: enforce single session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	refreshToken := uuid.New().String()
	if err := storeRefreshToken(ctx, h.kv, playerID, refreshToken); err != nil {
		log.Error().Err(err).Msg("auth: store refresh token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	success = true

	c.JSON(http.StatusOK, authResponse{
		JWT:          tokenStr,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
	})
}

func ticketHash(ticket string) string {
	h := sha256.Sum256([]byte(ticket))
	return hex.EncodeToString(h[:])
}

func upsertPlayer(ctx context.Context, db store.DB, steamID string) (string, error) {
	const q = `
		INSERT INTO players (id, steam_id, last_login)
		VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		ON CONFLICT (steam_id) DO UPDATE SET last_login = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		RETURNING id
	`
	var playerID string
	err := db.QueryRow(ctx, q, uuid.New().String(), steamID).Scan(&playerID)
	return playerID, err
}

func initPlayerState(ctx context.Context, db store.DB, playerID string) error {
	const q = `INSERT INTO player_states (player_id) VALUES (?) ON CONFLICT (player_id) DO NOTHING`
	_, err := db.Exec(ctx, q, playerID)
	return err
}

// updateLoginStreak applies daily-streak rules atomically based on UTC date:
//   - first login (NULL): streak = 1
//   - same UTC day: unchanged
//   - exactly previous UTC day: streak += 1
//   - any earlier date: streak resets to 1
//
// Single CASE-UPDATE avoids the read-modify-write race under concurrent logins.
func updateLoginStreak(ctx context.Context, db store.DB, playerID string) error {
	const q = `
		UPDATE player_states
		SET
		    streak_days = CASE
		        WHEN last_login_date IS NULL                        THEN 1
		        WHEN last_login_date = date('now')                  THEN streak_days
		        WHEN last_login_date = date('now', '-1 day')        THEN streak_days + 1
		        ELSE 1
		    END,
		    last_login_date = date('now'),
		    updated_at      = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		WHERE player_id = ?
	`
	_, err := db.Exec(ctx, q, playerID)
	return err
}
