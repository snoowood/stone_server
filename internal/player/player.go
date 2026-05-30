package player

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/gensdeis/stone-server/internal/cairn"
	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

type Handler struct {
	db       store.DB
	kv       kvstore.KVStore
	cairnCfg cairn.Config
}

func NewHandler(db store.DB, kv kvstore.KVStore, cairnCfg cairn.Config) *Handler {
	return &Handler{db: db, kv: kv, cairnCfg: cairnCfg}
}

type inventoryItem struct {
	ItemID     string    `json:"item_id"`
	Rarity     string    `json:"rarity"`
	Count      int       `json:"count"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// cairnStateResponse mirrors the server-authoritative WishCairn state.
// SlotCount / MaxLayers / SpawnIntervalSec 는 클라가 표시 외삽에 쓰는 게임 상수.
// Slots 는 매 read 시 (now - started_at) / interval 로 derive 된 슬롯 배열.
type cairnStateResponse struct {
	SlotCount        int               `json:"slot_count"`
	MaxLayers        int               `json:"max_layers"`
	SpawnIntervalSec int               `json:"spawn_interval_sec"`
	Slots            []cairn.SlotState `json:"slots"`
}

type stateResponse struct {
	PlayerID          string             `json:"player_id"`
	EnlightenmentPts  float64            `json:"enlightenment_pts"`
	EnlightenmentRate float64            `json:"enlightenment_rate"`
	TimeStoneCnt      int                `json:"time_stone_count"`
	StreakDays        int                `json:"streak_days"`
	NextGachaAt       *time.Time         `json:"next_gacha_at"`
	LastSyncAt        *time.Time         `json:"last_sync_at"`
	Inventory         []inventoryItem    `json:"inventory"`
	Cairn             cairnStateResponse `json:"cairn"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

type syncResponse struct {
	EnlightenmentPts float64    `json:"enlightenment_pts"`
	LastSyncAt       *time.Time `json:"last_sync_at"`
}

// Sync handles POST /api/v1/player/sync.
// passive gain 정산을 한 atomic SQL 안에서 처리 — /gacha/pull 의 accrual 과 동일 패턴.
// 동시 호출 시 같은 elapsed window 가 두 번 가산되는 race 방지.
func (h *Handler) Sync(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	var newPts float64
	var newSyncAt *time.Time
	// MAX(0, ...) 로 음수 elapsed (clock skew, 미래 last_sync_at) 시 잔고 감소 방지.
	err := h.db.QueryRow(ctx, `
		UPDATE player_states
		SET enlightenment_pts = enlightenment_pts +
		    MAX(0, COALESCE(
		        (CAST(strftime('%s','now') AS REAL) - CAST(strftime('%s', last_sync_at) AS REAL)) * enlightenment_rate,
		        0
		    )),
		    last_sync_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
		    updated_at   = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		WHERE player_id = ?
		RETURNING enlightenment_pts, last_sync_at
	`, playerID).Scan(&newPts, store.ScanNullTime(&newSyncAt))
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "player state not found", "code": "NOT_FOUND"})
		return
	}
	if err != nil {
		log.Error().Err(err).Msg("player sync: update enlightenment_pts")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	c.JSON(http.StatusOK, syncResponse{EnlightenmentPts: newPts, LastSyncAt: newSyncAt})
}

// GetState handles GET /api/v1/player/state.
func (h *Handler) GetState(c *gin.Context) {
	playerID := c.GetString("player_id")
	ctx := c.Request.Context()

	var resp stateResponse
	resp.PlayerID = playerID

	err := h.db.QueryRow(ctx, `
		SELECT enlightenment_pts, enlightenment_rate, time_stone_count, streak_days,
		       next_gacha_at, last_sync_at, updated_at
		FROM player_states
		WHERE player_id = ?
	`, playerID).Scan(
		&resp.EnlightenmentPts,
		&resp.EnlightenmentRate,
		&resp.TimeStoneCnt,
		&resp.StreakDays,
		store.ScanNullTime(&resp.NextGachaAt),
		store.ScanNullTime(&resp.LastSyncAt),
		store.ScanTime(&resp.UpdatedAt),
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "player state not found", "code": "NOT_FOUND"})
			return
		}
		log.Error().Err(err).Msg("player state: query player_states")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	rows, err := h.db.Query(ctx, `
		SELECT item_id, rarity, count, acquired_at
		FROM inventories
		WHERE player_id = ?
		ORDER BY acquired_at
	`, playerID)
	if err != nil {
		log.Error().Err(err).Msg("player state: query inventories")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	defer rows.Close()

	resp.Inventory = []inventoryItem{}
	for rows.Next() {
		var item inventoryItem
		if err := rows.Scan(&item.ItemID, &item.Rarity, &item.Count, store.ScanTime(&item.AcquiredAt)); err != nil {
			log.Error().Err(err).Msg("player state: scan inventory row")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}
		resp.Inventory = append(resp.Inventory, item)
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("player state: iterate inventories")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}

	// M2: WishCairn 슬롯 상태 — 매 read 시 derive.
	// 기존 player 가 M2 마이그레이션 이후 valid JWT 로 들어오는 경우 InitializeSlots 가
	// 안 거쳐졌을 수 있다 (initPlayerState 는 /auth/* 진입점에서만 호출). lazy init 보강.
	slots, err := h.cairnCfg.LoadSlots(ctx, h.db, playerID, time.Now())
	if err != nil {
		log.Error().Err(err).Msg("player state: load cairn slots")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	// partial init (1~4 슬롯만 있는 상태) 도 lazy 보강 — InitializeSlots 가 ON CONFLICT DO NOTHING
	// 멱등이라 빠진 인덱스만 채워짐.
	if len(slots) < h.cairnCfg.SlotCount {
		if err := h.cairnCfg.InitializeSlots(ctx, h.db, playerID, time.Now()); err != nil {
			log.Error().Err(err).Msg("player state: lazy init cairn slots")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}
		slots, err = h.cairnCfg.LoadSlots(ctx, h.db, playerID, time.Now())
		if err != nil {
			log.Error().Err(err).Msg("player state: reload cairn slots after lazy init")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}
	}
	resp.Cairn = cairnStateResponse{
		SlotCount:        h.cairnCfg.SlotCount,
		MaxLayers:        h.cairnCfg.MaxLayers,
		SpawnIntervalSec: h.cairnCfg.SpawnIntervalSeconds,
		Slots:            slots,
	}

	c.JSON(http.StatusOK, resp)
}
