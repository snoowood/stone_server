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
	"github.com/rs/zerolog"

	"github.com/gensdeis/stone-server/internal/cairn"
	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

const cooldownKeyFmt = "gacha:cooldown:%s"

type Handler struct {
	db       store.DB
	kv       kvstore.KVStore
	cfg      GameConfig
	cairnCfg cairn.Config
	pool     *SkinPool
}

func NewHandler(db store.DB, kv kvstore.KVStore, pool *SkinPool, cfg GameConfig, cairnCfg cairn.Config) *Handler {
	return &Handler{db: db, kv: kv, cfg: cfg, cairnCfg: cairnCfg, pool: pool}
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
	// DTO sync: grant provenance. SourceType 은 본 pull 의 출처(wish_cairn),
	// Reward 는 클라 RewardGrantDto 와 1:1 인 공통 grant 결과.
	SourceType string      `json:"source_type"`
	Reward     RewardGrant `json:"reward"`
}

var (
	errInsufficientPoints = errors.New("insufficient points")
	errCairnIncomplete    = errors.New("cairn slot not complete")
	errCairnSlotNotFound  = errors.New("cairn slot not found")
	errCooldownActive     = errors.New("gacha cooldown active")
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
		zerolog.Ctx(ctx).Error().Err(err).Msg("gacha status: cooldown check")
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
	if slotIndex < 0 || slotIndex >= h.cairnCfg.SlotCount {
		c.JSON(http.StatusBadRequest, gin.H{"error": "slot_index out of range", "code": "INVALID_SLOT"})
		return
	}

	nextAt, err := h.getNextGachaAt(ctx, playerID)
	if err != nil {
		zerolog.Ctx(ctx).Error().Err(err).Msg("gacha pull: cooldown check")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	if nextAt != nil && time.Now().Before(*nextAt) {
		zerolog.Ctx(ctx).Debug().Str("code", "COOLDOWN_ACTIVE").Msg("gacha pull rejected")
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":         "gacha cooldown active",
			"code":          "COOLDOWN_ACTIVE",
			"next_gacha_at": nextAt,
		})
		return
	}

	res, err := h.execPull(ctx, playerID, slotIndex)
	if err != nil {
		lg := zerolog.Ctx(ctx)
		switch {
		case errors.Is(err, errCooldownActive):
			// 트랜잭션 내부 쿨다운 게이트(step 0a)가 동시 pull 경합으로 거절. pre-tx 와 동일 형태로 응답.
			// LOG-7: 동시 pull 경합 신호라 Info 로 승격(정상 pre-tx 쿨다운 거절은 Debug 유지).
			lg.Info().Str("code", "COOLDOWN_ACTIVE").Msg("gacha pull rejected (tx gate)")
			nextAt, _ := h.getNextGachaAt(ctx, playerID)
			body := gin.H{"error": "gacha cooldown active", "code": "COOLDOWN_ACTIVE"}
			if nextAt != nil {
				body["next_gacha_at"] = nextAt
			}
			c.JSON(http.StatusTooManyRequests, body)
			return
		case errors.Is(err, errInsufficientPoints):
			lg.Debug().Str("code", "INSUFFICIENT_POINTS").Msg("gacha pull rejected")
			c.JSON(http.StatusConflict, gin.H{"error": "insufficient enlightenment points", "code": "INSUFFICIENT_POINTS"})
			return
		case errors.Is(err, errCairnIncomplete):
			// 슬롯 미완성 상태의 정상 조기 요청에도 나오는 거절이라 Debug 유지(경합 전용 아님).
			lg.Debug().Str("code", "CAIRN_INCOMPLETE").Msg("gacha pull rejected")
			c.JSON(http.StatusForbidden, gin.H{"error": "cairn slot not complete", "code": "CAIRN_INCOMPLETE"})
			return
		case errors.Is(err, errCairnSlotNotFound):
			lg.Debug().Str("code", "CAIRN_SLOT_NOT_FOUND").Msg("gacha pull rejected")
			c.JSON(http.StatusNotFound, gin.H{"error": "cairn slot not found", "code": "CAIRN_SLOT_NOT_FOUND"})
			return
		}
		lg.Error().Err(err).Msg("gacha pull: transaction")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	h.setCooldown(ctx, playerID, res.NextGachaAt)

	// LOG-4: one structured economy line per committed pull (post-commit, off the
	// hot path, dependency-0). request_id/player_id come from the bound logger.
	// Makes spend/drop-rate/dup-rate observable without scraping gacha_logs.
	zerolog.Ctx(ctx).Info().
		Str("event", "gacha_pull").
		Str("item_id", res.ItemID).
		Str("rarity", res.Rarity).
		Bool("is_duplicate", res.IsDuplicate).
		Float64("cost_points", h.cfg.PullCost).
		Float64("balance_after", res.BalanceAfter).
		Int("new_count", res.NewCount).
		Msg("gacha pull committed")

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
	nextGachaAt := now.Add(h.cfg.Cooldown)

	// 0a. 쿨다운 CAS 게이트 (트랜잭션 내부). next_gacha_at 을 now+cooldown 으로 claim 하되
	//     아직 쿨다운이 끝난(또는 최초인) 경우에만 affected=1. pre-tx 검사(Pull 핸들러)는
	//     fast-path 일 뿐, 서로 다른 complete 슬롯에 대한 동시 pull 이 pre-check 를 둘 다
	//     통과한 뒤 이 게이트로 들어오면 먼저 커밋한 1건만 통과하고 나머지는 affected=0 →
	//     errCooldownActive. 실패 시 defer tx.Rollback 으로 슬롯/포인트 소모도 함께 롤백된다.
	gateRes, err := tx.Exec(ctx,
		`UPDATE player_states
		   SET next_gacha_at = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		 WHERE player_id = ?
		   AND (next_gacha_at IS NULL OR next_gacha_at <= ?)`,
		nextGachaAt.Format(time.RFC3339), playerID, nowStr,
	)
	if err != nil {
		return nil, err
	}
	if affected, _ := gateRes.RowsAffected(); affected == 0 {
		return nil, errCooldownActive
	}

	// 0. WishCairn slot 검증 + reset 을 atomic CAS UPDATE 로. complete 조건 (started_at <=
	//    now - MaxLayers × interval) 인 슬롯만 reset. 동시 두 요청이 같은 slot 을 claim
	//    하려고 해도 한 쪽만 affected=1, 다른 쪽은 affected=0 → CAIRN_INCOMPLETE.
	threshold := now.Add(-time.Duration(h.cairnCfg.MaxLayers*h.cairnCfg.SpawnIntervalSeconds) * time.Second)
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

	// LOG-5: capture the pre-mutation balance for the gacha_logs audit trail. This is a
	// read-only snapshot — it is NOT fed back into the accrue/deduct math below, so it does
	// not reintroduce the double-accrual race the next step guards against (the balance
	// change still happens atomically in one UPDATE). SQLite serializes writes
	// (MaxOpenConns=1), so no other write lands between this SELECT and the UPDATE.
	// (PG path, post R1(B): this read needs SELECT ... FOR UPDATE.)
	var balanceBefore float64
	if err := tx.QueryRow(ctx,
		"SELECT enlightenment_pts FROM player_states WHERE player_id = ?", playerID,
	).Scan(&balanceBefore); err != nil {
		return nil, err
	}

	// 1+2. Atomic accrue + deduct + last_sync_at 갱신을 한 UPDATE 로.
	//      별도 SELECT → 코드 계산 → UPDATE 분리하면 동일 elapsed window 를 두 번 가산할 race
	//      가능 (e.g. /player/sync 와 동시). 한 SQL 안에서 pending 을 계산해 atomic 보장.
	//      SQLite strftime/CAST 사용 — 기존 코드와 동일 패턴 (SQLite-우선).
	// MAX(0, ...) 로 음수 elapsed (clock skew) 시 잔고 감소 방지.
	var newBalance float64
	var lastSyncAt time.Time
	err = tx.QueryRow(ctx, `
		UPDATE player_states
		SET enlightenment_pts = enlightenment_pts +
			MAX(0, COALESCE(
				(CAST(strftime('%s','now') AS REAL) - CAST(strftime('%s', last_sync_at) AS REAL)) * enlightenment_rate,
				0
			)) - ?,
		    last_sync_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE player_id = ?
		  AND enlightenment_pts +
		      MAX(0, COALESCE(
		          (CAST(strftime('%s','now') AS REAL) - CAST(strftime('%s', last_sync_at) AS REAL)) * enlightenment_rate,
		          0
		      )) >= ?
		RETURNING enlightenment_pts, last_sync_at
	`, h.cfg.PullCost, playerID, h.cfg.PullCost).Scan(&newBalance, store.ScanTime(&lastSyncAt))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInsufficientPoints
	}
	if err != nil {
		return nil, err
	}

	// LOG-5: accrued = balance_after - balance_before + cost, recovering the passive
	// accrual the atomic UPDATE folded in (its MAX(0,...) already clamped negative elapsed).
	accrued := newBalance - balanceBefore + h.cfg.PullCost

	// 3. RNG
	result, err := h.pool.Roll()
	if err != nil {
		return nil, err
	}

	// 4. Inventory grant — 공용 GrantItem 빌더로 집계형 stack UPSERT.
	//    item_type/source_type provenance 까지 기록하고 RewardGrant 를 받아 응답에 싣는다.
	//    is_duplicate/new_count 는 grant 의 RETURNING count 로 판정.
	grant, err := GrantItem(ctx, tx, playerID, result.ItemID, string(result.Rarity), ItemTypeStoneSkin, SourceWishCairn)
	if err != nil {
		return nil, err
	}
	newCount := grant.NewCount
	isDuplicate := grant.IsDuplicate

	// 5. (제거됨) refund 폐지 — M3 집계형은 중복 그 자체가 가치. RefundPoints 는
	//    호환성 유지를 위해 응답 필드로 남기지만 항상 0.
	refundPts := 0.0

	// 6. next_gacha_at 은 step 0a 의 쿨다운 CAS 게이트가 이미 now+cooldown 으로 설정함
	//    (last_sync_at 은 step 2, 슬롯 reset 은 step 0 에서 atomic 처리). 별도 UPDATE 불필요.

	// 7. Append gacha log
	if _, err := tx.Exec(ctx,
		`INSERT INTO gacha_logs
		    (id, player_id, item_id, rarity, is_duplicate, cost_points, refund_points, gacha_seed_hash,
		     balance_before, balance_after, accrued_pts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), playerID, result.ItemID, string(result.Rarity),
		isDuplicate, h.cfg.PullCost, refundPts, result.SeedHash,
		balanceBefore, newBalance, accrued,
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
		SourceType:   SourceWishCairn,
		Reward:       grant,
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
		zerolog.Ctx(ctx).Error().Err(err).Msg("gacha logs: count")
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
		zerolog.Ctx(ctx).Error().Err(err).Msg("gacha logs: query")
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
			zerolog.Ctx(ctx).Error().Err(err).Msg("gacha logs: scan")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}
		logs = append(logs, e)
	}
	if err := rows.Err(); err != nil {
		zerolog.Ctx(ctx).Error().Err(err).Msg("gacha logs: iterate")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, logsResponse{Logs: logs, Total: total})
}

func (h *Handler) setCooldown(ctx context.Context, playerID string, nextAt time.Time) {
	key := fmt.Sprintf(cooldownKeyFmt, playerID)
	h.kv.Set(ctx, key, nextAt.Format(time.RFC3339), h.cfg.Cooldown)
}
