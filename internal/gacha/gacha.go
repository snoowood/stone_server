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

	"github.com/gensdeis/stone-server/internal/cairn"
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
	RefundPoints float64   `json:"refund_points"` // M3: 항상 0 (refund 폐지, 집계형 stack 모델). 호환성 유지.
	NextGachaAt  time.Time `json:"next_gacha_at"`
	// M1: 권위 잔고와 권위 sync 시각.
	BalanceAfter float64   `json:"balance_after"`
	LastSyncAt   time.Time `json:"last_sync_at"`
	// M3: 가챠 후 해당 item_id 의 누적 보유 개수. 신규면 1, 중복이면 K+1.
	NewCount int `json:"new_count"`
}

var (
	errInsufficientPoints = errors.New("insufficient points")
	errCairnIncomplete    = errors.New("cairn slot not complete")
	errCairnSlotNotFound  = errors.New("cairn slot not found")
)

// pullRequest mirrors the POST /api/v1/gacha/pull body.
// M2: slot_index 필수. 서버가 해당 슬롯이 complete 인지 검증.
type pullRequest struct {
	SlotIndex *int `json:"slot_index" binding:"required"`
}

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

	var req pullRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.SlotIndex == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slot_index is required", "code": "INVALID_REQUEST"})
		return
	}
	slotIndex := *req.SlotIndex
	if slotIndex < 0 || slotIndex >= cairn.SlotCount {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slot_index out of range", "code": "INVALID_SLOT"})
		return
	}

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

	res, err := h.execPull(ctx, playerID, slotIndex)
	if err != nil {
		switch {
		case errors.Is(err, errInsufficientPoints):
			c.JSON(http.StatusConflict, gin.H{"error": "insufficient enlightenment points", "code": "INSUFFICIENT_POINTS"})
			return
		case errors.Is(err, errCairnIncomplete):
			c.JSON(http.StatusForbidden, gin.H{"error": "cairn slot not complete", "code": "CAIRN_INCOMPLETE"})
			return
		case errors.Is(err, errCairnSlotNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "cairn slot not found", "code": "CAIRN_SLOT_NOT_FOUND"})
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

func (h *Handler) execPull(ctx context.Context, playerID string, slotIndex int) (*pullResponse, error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC().Truncate(time.Second)
	nowStr := now.Format(time.RFC3339)

	// 0. WishCairn slot 검증 + reset 을 atomic CAS UPDATE 로. complete 조건 (started_at <=
	//    now - MaxLayers × interval) 인 슬롯만 reset. 동시 두 요청이 같은 slot 을 claim
	//    하려고 해도 한 쪽만 affected=1, 다른 쪽은 affected=0 → CAIRN_INCOMPLETE.
	threshold := now.Add(-time.Duration(cairn.MaxLayers*cairn.SpawnIntervalSeconds) * time.Second)
	res, err := tx.Exec(ctx,
		"UPDATE wish_cairn_slots SET started_at = ?, claimed_at = ? WHERE player_id = ? AND slot_index = ? AND started_at <= ?",
		nowStr, nowStr, playerID, slotIndex, threshold.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		// slot 존재 여부 확인으로 INCOMPLETE / NOT_FOUND 구분.
		_, err := cairn.LoadSlotStartedAt(ctx, tx, playerID, slotIndex)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errCairnSlotNotFound
		}
		if err != nil {
			return nil, err
		}
		return nil, errCairnIncomplete
	}

	// 1+2. Atomic accrue + deduct + last_sync_at 갱신을 한 UPDATE 로.
	//      별도 SELECT → 코드 계산 → UPDATE 분리하면 동일 elapsed window 를 두 번 가산할 race
	//      가능 (e.g. /player/sync 와 동시). 한 SQL 안에서 pending 을 계산해 atomic 보장.
	//      SQLite strftime/CAST 사용 — 기존 코드와 동일 패턴 (SQLite-우선).
	var newBalance float64
	var lastSyncAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE player_states
		SET enlightenment_pts = enlightenment_pts +
			COALESCE(
				(CAST(strftime('%s','now') AS REAL) - CAST(strftime('%s', last_sync_at) AS REAL)) * enlightenment_rate,
				0
			) - ?,
		    last_sync_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE player_id = ?
		  AND enlightenment_pts +
		      COALESCE(
		          (CAST(strftime('%s','now') AS REAL) - CAST(strftime('%s', last_sync_at) AS REAL)) * enlightenment_rate,
		          0
		      ) >= ?
		RETURNING enlightenment_pts, last_sync_at
	`, h.cfg.PullCost, playerID, h.cfg.PullCost).Scan(&newBalance, store.ScanTime(&lastSyncAt))
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

	// 4. Inventory UPSERT — M3: 집계형 stack 모델.
	//    중복이면 count += 1, 신규면 count = 1. RETURNING count 로 결과를 받아 응답에 포함.
	//    is_duplicate 는 RETURNING 의 count 가 1 인지(=신규)/>1 인지(=중복) 로 판정.
	var newCount int
	err = tx.QueryRow(ctx, `
		INSERT INTO inventories (id, player_id, item_id, rarity, count)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT (player_id, item_id) DO UPDATE SET count = inventories.count + 1
		RETURNING count
	`, uuid.New().String(), playerID, result.ItemID, string(result.Rarity)).Scan(&newCount)
	if err != nil {
		return nil, err
	}
	isDuplicate := newCount > 1

	// 5. (제거됨) refund 폐지 — M3 집계형은 중복 그 자체가 가치. RefundPoints 는
	//    호환성 유지를 위해 응답 필드로 남기지만 항상 0.
	refundPts := 0.0

	// 6. Set next_gacha_at. last_sync_at 은 step 2 의 atomic UPDATE 가 갱신함.
	//    슬롯 reset 은 step 0 에서 atomic CAS 로 완료.
	nextGachaAt := now.Add(cooldownTTL)
	if _, err := tx.Exec(ctx,
		"UPDATE player_states SET next_gacha_at = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE player_id = ?",
		nextGachaAt.Format(time.RFC3339), playerID,
	); err != nil {
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
		NewCount:     newCount,
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
