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

	"github.com/gensdeis/stone-server/internal/cairn"
	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

// Handler handles Steam authentication requests.
type Handler struct {
	db       store.DB
	kv       kvstore.KVStore
	steam    SteamClient
	privKey  *rsa.PrivateKey
	cairnCfg cairn.Config
}

// NewHandler creates a Handler with the given dependencies.
func NewHandler(db store.DB, kv kvstore.KVStore, steam SteamClient, privKey *rsa.PrivateKey, cairnCfg cairn.Config) *Handler {
	return &Handler{db: db, kv: kv, steam: steam, privKey: privKey, cairnCfg: cairnCfg}
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
		// claimTicket 은 KV SetNX(쓰기) — 실패는 순수 인프라 장애(prod=Redis 도달불가)이지 인증
		// 판정이 아니므로 500 대신 503+Retry-After 로 페이싱(SRE-5 를 인증 전구간으로 일관화, handoff §8).
		log.Error().Err(err).Msg("auth: claim ticket")
		c.Header("Retry-After", "5")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service temporarily unavailable", "code": "SERVICE_UNAVAILABLE"})
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
			// 거절 사유(무효/밴/가족공유)는 로그로 구분하되, 응답은 기존 INVALID_TICKET 으로
			// 통일해 클라 계약 변경을 피한다.
			log.Warn().Err(err).Msg("auth: steam ticket rejected")
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

	if err := initPlayerState(ctx, h.db, h.cairnCfg, playerID); err != nil {
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
		c.Header("Retry-After", "5")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service temporarily unavailable", "code": "SERVICE_UNAVAILABLE"})
		return
	}

	// ECON-3: session won — re-anchor the accrual clock so the offline gap is dropped.
	// Placed after enforceSingleSession so a rejected re-login leaves the anchor intact.
	if err := resetAccrualAnchor(ctx, h.db, playerID); err != nil {
		log.Error().Err(err).Str("player_id", playerID).Msg("auth: reset accrual anchor")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	refreshToken := uuid.New().String()
	if err := storeRefreshToken(ctx, h.kv, playerID, refreshToken); err != nil {
		log.Error().Err(err).Msg("auth: store refresh token")
		c.Header("Retry-After", "5")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service temporarily unavailable", "code": "SERVICE_UNAVAILABLE"})
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

func initPlayerState(ctx context.Context, db store.DB, cairnCfg cairn.Config, playerID string) error {
	// E2E-3: anchor last_sync_at = now at creation. With a NULL last_sync_at the
	// passive-accrual formula MAX(0, COALESCE(now - last_sync_at, 0) * rate) yields 0,
	// so a brand-new account accrues nothing and its first gacha (which fires before
	// the first /player/sync) hits 409 INSUFFICIENT_POINTS. Anchoring at creation lets
	// accrual start from t0. ON CONFLICT DO NOTHING keeps this a pure idempotent row
	// create — it must NOT move an existing anchor. The ECON-3 re-login reset of
	// last_sync_at is applied separately by resetAccrualAnchor, only AFTER the session
	// is established, so a rejected/failed re-login cannot mutate an active session's
	// anchor. Format matches upsertPlayer/gacha so strftime('%s', …) parses it.
	const q = `INSERT INTO player_states (player_id, last_sync_at)
	           VALUES (?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	           ON CONFLICT (player_id) DO NOTHING`
	if _, err := db.Exec(ctx, q, playerID); err != nil {
		return err
	}
	// M2: 신규 플레이어용 WishCairn 슬롯도 같이 초기화 (phase_offset 으로 시차 부여).
	// ON CONFLICT DO NOTHING 이라 재로그인 시에는 no-op.
	return cairnCfg.InitializeSlots(ctx, db, playerID, time.Now())
}

// resetAccrualAnchor implements ECON-3: on session start it re-anchors last_sync_at
// = now so the closed-app (offline) window is dropped from passive accrual. The
// balance (enlightenment_pts) is left untouched — the offline gap is discarded, not
// credited; online idle keeps accruing via the in-session /player/sync (5min) loop.
//
// Must be called only AFTER enforceSingleSession succeeds. A rejected re-login
// (LOGIN_IN_PROGRESS) or a login that fails earlier never reaches this, so it cannot
// move the anchor of an already-active session. /auth/refresh deliberately does not
// call this — a mid-session token refresh must not drop online accrual.
func resetAccrualAnchor(ctx context.Context, db store.DB, playerID string) error {
	const q = `UPDATE player_states
	              SET last_sync_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
	                  updated_at   = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
	            WHERE player_id = ?`
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
