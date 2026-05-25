package gacha

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

const (
	cooldownTTL    = 30 * time.Minute
	cooldownKeyFmt = "gacha:cooldown:%s"
)

type Handler struct {
	db  store.DB
	kv  kvstore.KVStore
	cfg GameConfig
}

func NewHandler(db store.DB, kv kvstore.KVStore) *Handler {
	return &Handler{db: db, kv: kv, cfg: DefaultConfig}
}

type statusResponse struct {
	CanPull     bool       `json:"can_pull"`
	NextGachaAt *time.Time `json:"next_gacha_at"`
}

type pullResponse struct {
	ItemID       string    `json:"item_id"`
	Rarity       string    `json:"rarity"`
	IsDuplicate  bool      `json:"is_duplicate"`
	RefundPoints float64   `json:"refund_points"`
	NextGachaAt  time.Time `json:"next_gacha_at"`
	// M1: 권위 잔고와 권위 sync 시각. 클라가 BalanceAfter 로 자기 표시 잔고를
	// 덮어쓰고, LastSyncAt 을 다음 외삽의 anchor 로 사용한다.
	BalanceAfter float64   `json:"balance_after"`
	LastSyncAt   time.Time `json:"last_sync_at"`
}

var errInsufficientPoints = errors.New("insufficient points")

// Status handles GET /api/v1/gacha/status.
func (h *Handler) Status(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	nextAt, err := h.getNextGachaAt(ctx, playerID)
	if err != nil {
		log.Error().Err(err).Msg("gacha status: cooldown check")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	canPull := nextAt == nil || !time.Now().Before(*nextAt)
	var responseNextAt *time.Time
	if !canPull {
		responseNextAt = nextAt
	}

	c.JSON(http.StatusOK, statusResponse{
		CanPull:     canPull,
		NextGachaAt: responseNextAt,
	})
}

// Pull handles POST /api/v1/gacha/pull.
func (h *Handler) Pull(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	nextAt, err := h.getNextGachaAt(ctx, playerID)
	if err != nil {
		log.Error().Err(err).Msg("gacha pull: cooldown check")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	if nextAt != nil && time.Now().Before(*nextAt) {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":         "gacha cooldown active",
			"code":          "COOLDOWN_ACTIVE",
			"next_gacha_at": nextAt,
		})
		return
	}

	res, err := h.execPull(ctx, playerID)
	if err != nil {
		if errors.Is(err, errInsufficientPoints) {
			c.JSON(http.StatusConflict, gin.H{"error": "insufficient enlightenment points", "code": "INSUFFICIENT_POINTS"})
			return
		}
		log.Error().Err(err).Msg("gacha pull: transaction")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	h.setCooldown(ctx, playerID, res.NextGachaAt)
	c.JSON(http.StatusOK, res)
}

// getNextGachaAt checks the KV store first; falls back to DB on cache miss.
func (h *Handler) getNextGachaAt(ctx context.Context, playerID string) (*time.Time, error) {
	key := fmt.Sprintf(cooldownKeyFmt, playerID)

	val, err := h.kv.Get(ctx, key)
	if err == nil {
		if t, parseErr := time.Parse(time.RFC3339, val); parseErr == nil {
			return &t, nil
		}
	} else if !errors.Is(err, kvstore.ErrNotFound) {
		return nil, err
	}

	// Cache miss: fallback to DB
	var nextAt *time.Time
	if scanErr := h.db.QueryRow(ctx,
		"SELECT next_gacha_at FROM player_states WHERE player_id = ?", playerID,
	).Scan(store.ScanNullTime(&nextAt)); scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
		return nil, scanErr
	}

	if nextAt != nil && time.Now().Before(*nextAt) {
		h.kv.Set(ctx, key, nextAt.Format(time.RFC3339), time.Until(*nextAt))
	}

	return nextAt, nil
}

func (h *Handler) execPull(ctx context.Context, playerID string) (*pullResponse, error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// 1+2. Atomic read-and-deduct. WHERE pts >= cost guarantees no overdraft
	// even under concurrent pulls in PostgreSQL (row-lock) or SQLite (serial writes).
	var newBalance float64
	err = tx.QueryRow(ctx, `
		UPDATE player_states
		SET enlightenment_pts = enlightenment_pts - ?
		WHERE player_id = ? AND enlightenment_pts >= ?
		RETURNING enlightenment_pts
	`, h.cfg.PullCost, playerID, h.cfg.PullCost).Scan(&newBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInsufficientPoints
	}
	if err != nil {
		return nil, err
	}

	// 3. RNG
	result, err := Roll()
	if err != nil {
		return nil, err
	}

	// 4. Inventory UPSERT
	res, err := tx.Exec(ctx,
		"INSERT INTO inventories (id, player_id, item_id, rarity) VALUES (?, ?, ?, ?) ON CONFLICT (player_id, item_id) DO NOTHING",
		uuid.New().String(), playerID, result.ItemID, string(result.Rarity),
	)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	isDuplicate := affected == 0

	// 5. Refund on duplicate — RETURNING 으로 newBalance 갱신해서 응답 권위값에 반영.
	refundPts := 0.0
	if isDuplicate {
		refundPts = h.cfg.RefundPts[result.Rarity]
		if refundPts > 0 {
			if err := tx.QueryRow(ctx,
				"UPDATE player_states SET enlightenment_pts = enlightenment_pts + ? WHERE player_id = ? RETURNING enlightenment_pts",
				refundPts, playerID,
			).Scan(&newBalance); err != nil {
				return nil, err
			}
		}
	}

	// 6. Set next_gacha_at + last_sync_at. last_sync_at 갱신으로 /player/sync 가 호출되지
	//    않아도 클라 외삽 anchor 가 가챠 시점으로 전진한다.
	nextGachaAt := time.Now().Add(cooldownTTL).UTC().Truncate(time.Second)
	var lastSyncAt time.Time
	if err := tx.QueryRow(ctx,
		"UPDATE player_states SET next_gacha_at = ?, last_sync_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE player_id = ? RETURNING last_sync_at",
		nextGachaAt.Format(time.RFC3339), playerID,
	).Scan(store.ScanTime(&lastSyncAt)); err != nil {
		return nil, err
	}

	// 7. Append gacha log
	if _, err := tx.Exec(ctx,
		`INSERT INTO gacha_logs
		    (id, player_id, item_id, rarity, is_duplicate, cost_points, refund_points, gacha_seed_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), playerID, result.ItemID, string(result.Rarity),
		isDuplicate, h.cfg.PullCost, refundPts, result.SeedHash,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &pullResponse{
		ItemID:       result.ItemID,
		Rarity:       string(result.Rarity),
		IsDuplicate:  isDuplicate,
		RefundPoints: refundPts,
		NextGachaAt:  nextGachaAt,
		BalanceAfter: newBalance,
		LastSyncAt:   lastSyncAt,
	}, nil
}

const (
	defaultLogsLimit = 20
	maxLogsLimit     = 100
)

type logEntry struct {
	ItemID       string    `json:"item_id"`
	Rarity       string    `json:"rarity"`
	IsDuplicate  bool      `json:"is_duplicate"`
	RefundPoints float64   `json:"refund_points"`
	CostPoints   float64   `json:"cost_points"`
	PulledAt     time.Time `json:"pulled_at"`
}

type logsResponse struct {
	Logs  []logEntry `json:"logs"`
	Total int64      `json:"total"`
}

// Logs handles GET /api/v1/gacha/logs?page=1&limit=20.
func (h *Handler) Logs(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > maxLogsLimit {
		limit = defaultLogsLimit
	}
	offset := (page - 1) * limit

	var total int64
	if err := h.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM gacha_logs WHERE player_id = ?", playerID,
	).Scan(&total); err != nil {
		log.Error().Err(err).Msg("gacha logs: count")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT item_id, rarity, is_duplicate, refund_points, cost_points, pulled_at
		 FROM gacha_logs
		 WHERE player_id = ?
		 ORDER BY pulled_at DESC
		 LIMIT ? OFFSET ?`,
		playerID, limit, offset,
	)
	if err != nil {
		log.Error().Err(err).Msg("gacha logs: query")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	logs := []logEntry{}
	for rows.Next() {
		var e logEntry
		if err := rows.Scan(
			&e.ItemID, &e.Rarity, &e.IsDuplicate,
			&e.RefundPoints, &e.CostPoints, store.ScanTime(&e.PulledAt),
		); err != nil {
			log.Error().Err(err).Msg("gacha logs: scan")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}
		logs = append(logs, e)
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("gacha logs: iterate")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, logsResponse{Logs: logs, Total: total})
}

func (h *Handler) setCooldown(ctx context.Context, playerID string, nextAt time.Time) {
	key := fmt.Sprintf(cooldownKeyFmt, playerID)
	h.kv.Set(ctx, key, nextAt.Format(time.RFC3339), cooldownTTL)
}
