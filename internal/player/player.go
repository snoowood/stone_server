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
	ItemType   string    `json:"item_type"`
	Rarity     string    `json:"rarity"`
	Count      int       `json:"count"`
	SourceType string    `json:"source_type"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// cairnStateResponse mirrors the server-authoritative WishCairn state.
// SlotCount / MaxLayers / SpawnIntervalSec 는 클라 표시용 게임 상수. issue #34 저장형 전환
// 후 SpawnIntervalSec 의미가 "슬롯당 성장 간격"→"전역 랜덤 스텝당 간격"(interval 마다 미완성
// 슬롯 중 무작위 1곳 +1)으로 바뀌었다. Slots 는 서버가 저장한 layer_count 스냅샷이다.
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

	// issue #34: sync 도 "접속 중" 신호이므로 저장형 cairn 성장을 lazy 로 적립한다(응답
	// 스키마 무변경 — cairn 은 /player/state 에서만 내려간다). accrual UPDATE 는 이미 커밋됐고
	// 404(플레이어 없음) 판정도 위에서 끝났으므로, 여기 성장은 best-effort 다: 실패해도 sync
	// 성공(200)을 뒤집지 않고 다음 요청에서 재시도한다(앵커는 CAS 실패 시 불변).
	if err := h.cairnCfg.ApplyGrowthTx(ctx, h.db, playerID, time.Now()); err != nil {
		log.Warn().Err(err).Str("player_id", playerID).Msg("player sync: apply cairn growth (best-effort)")
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
		SELECT item_id, rarity, count, acquired_at, item_type, source_type
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
		if err := rows.Scan(&item.ItemID, &item.Rarity, &item.Count, store.ScanTime(&item.AcquiredAt), &item.ItemType, &item.SourceType); err != nil {
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

	// issue #34: WishCairn 저장형 슬롯 상태. 순서 = 슬롯 존재 보장 → ApplyGrowth(성장 적립)
	// → LoadSlots(성장 반영된 저장 스냅샷).
	// 기존 player 가 M2 마이그레이션 이후 valid JWT 로 들어오는 경우 InitializeSlots 가
	// 안 거쳐졌을 수 있다 (initPlayerState 는 /auth/* 진입점에서만 호출). lazy init 보강.
	slots, err := h.cairnCfg.LoadSlots(ctx, h.db, playerID)
	if err != nil {
		log.Error().Err(err).Msg("player state: load cairn slots")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	// partial init (1~4 슬롯만 있는 상태) 도 lazy 보강 — InitializeSlots 가 ON CONFLICT DO NOTHING
	// 멱등이라 빠진 인덱스만 채워짐. 성장 전에 슬롯 존재를 보장한다.
	if len(slots) < h.cairnCfg.SlotCount {
		if err := h.cairnCfg.InitializeSlots(ctx, h.db, playerID, time.Now()); err != nil {
			log.Error().Err(err).Msg("player state: lazy init cairn slots")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}
	}
	// 저장형 성장 lazy 적용 — tx 로 감싸 read→CAS→write 원자성/동시성 안전 확보.
	if err := h.cairnCfg.ApplyGrowthTx(ctx, h.db, playerID, time.Now()); err != nil {
		log.Error().Err(err).Msg("player state: apply cairn growth")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	// 성장 반영된 최종 저장 스냅샷 로드.
	slots, err = h.cairnCfg.LoadSlots(ctx, h.db, playerID)
	if err != nil {
		log.Error().Err(err).Msg("player state: reload cairn slots after growth")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
		return
	}
	resp.Cairn = cairnStateResponse{
		SlotCount:        h.cairnCfg.SlotCount,
		MaxLayers:        h.cairnCfg.MaxLayers,
		SpawnIntervalSec: h.cairnCfg.SpawnIntervalSeconds,
		Slots:            slots,
	}

	c.JSON(http.StatusOK, resp)
}
