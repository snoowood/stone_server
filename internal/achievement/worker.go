package achievement

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

const retryQueue = "ach:retry"

// DefaultMaxRetries bounds how many times a single achievement sync is retried
// before it is dead-lettered (left in achievement_retry_queue with
// retry_count == DefaultMaxRetries and resolved_at IS NULL — a queryable terminal
// state) instead of being re-queued forever.
const DefaultMaxRetries = 10

type workerDB interface {
	QueryRow(ctx context.Context, query string, args ...any) store.Row
	Exec(ctx context.Context, query string, args ...any) (store.Result, error)
}

type Worker struct {
	db           workerDB
	kv           kvstore.KVStore
	steam        SteamAchievementClient
	tickInterval time.Duration
	maxRetries   int
}

func NewWorker(db store.DB, kv kvstore.KVStore, steam SteamAchievementClient) *Worker {
	return &Worker{db: db, kv: kv, steam: steam, tickInterval: time.Minute, maxRetries: DefaultMaxRetries}
}

// ReloadPendingRetries reconciles the KV retry queue with achievement_retry_queue at
// boot, treating the DB as the source of truth. It rebuilds the queue to exactly the
// set of pending rows (resolved_at IS NULL AND retry_count < maxRetries): it queries
// that set first (so a query failure leaves the existing queue untouched), drains the
// queue, then re-enqueues the pending set.
//
// This both (a) recovers the dev MemStore queue, which is wiped on every restart, and
// (b) recovers rows orphaned in prod when a crash lands between the worker's RPop and
// its deferred RPush — a coarse "skip if queue non-empty" guard would miss those. The
// drain+rebuild also avoids double-enqueuing rows already present. Returns the number
// of rows re-enqueued. Safe because it runs before the worker and HTTP server start, so
// nothing else touches the queue concurrently.
func ReloadPendingRetries(ctx context.Context, db store.DB, kv kvstore.KVStore, maxRetries int) (int, error) {
	rows, err := db.Query(ctx,
		`SELECT player_id, achievement_id FROM achievement_retry_queue
		 WHERE resolved_at IS NULL AND retry_count < ?
		 ORDER BY next_retry_at`, maxRetries)
	if err != nil {
		return 0, err
	}
	var items []string
	for rows.Next() {
		var playerID, achievementID string
		if err := rows.Scan(&playerID, &achievementID); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, playerID+":"+achievementID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// Drain whatever survived (dev: already empty; prod: a stale subset/superset).
	for {
		if _, err := kv.RPop(ctx, retryQueue); errors.Is(err, kvstore.ErrNotFound) {
			break
		} else if err != nil {
			return 0, err
		}
	}
	// Re-enqueue in next_retry_at order: LPush in order → RPop yields the same order.
	for _, item := range items {
		if err := kv.LPush(ctx, retryQueue, item); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// Start launches the retry worker goroutine.
func (w *Worker) Start(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			w.run(ctx)
			select {
			case <-ctx.Done():
				return
			default:
				log.Warn().Msg("achievement worker: restarting after panic")
				time.Sleep(5 * time.Second)
			}
		}
	}()
}

func (w *Worker) run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("achievement worker: recovered from panic")
		}
	}()

	ticker := time.NewTicker(w.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

func (w *Worker) processBatch(ctx context.Context) {
	var toRequeue []string

	for {
		item, err := w.kv.RPop(ctx, retryQueue)
		if errors.Is(err, kvstore.ErrNotFound) {
			break
		}
		if err != nil {
			log.Error().Err(err).Msg("achievement worker: rpop failed")
			break
		}
		if w.processOne(ctx, item) {
			toRequeue = append(toRequeue, item)
		}
	}

	// Re-queue failed items at the tail (RPush) so they don't leapfrog newer
	// items LPush'd by the producer during this batch. Reverse iteration
	// preserves the original pop order on the next RPop pass.
	for i := len(toRequeue) - 1; i >= 0; i-- {
		w.kv.RPush(ctx, retryQueue, toRequeue[i])
	}
}

func (w *Worker) processOne(ctx context.Context, item string) (requeue bool) {
	parts := strings.SplitN(item, ":", 2)
	if len(parts) != 2 {
		log.Error().Str("item", item).Msg("achievement worker: malformed item, dropping")
		return false
	}
	playerID, achievementID := parts[0], parts[1]

	var steamID string
	err := w.db.QueryRow(ctx, "SELECT steam_id FROM players WHERE id = ?", playerID).Scan(&steamID)
	if errors.Is(err, sql.ErrNoRows) {
		log.Error().Str("player_id", playerID).Msg("achievement worker: player not found, dropping item")
		return false
	}
	if err != nil {
		log.Error().Err(err).Str("player_id", playerID).Msg("achievement worker: lookup steam_id, re-queuing")
		return true
	}

	if err := w.steam.SetAchievement(ctx, steamID, achievementID); err != nil {
		nextRetry := time.Now().Add(w.tickInterval).UTC()
		// LOG-3: bound retries. Increment under `retry_count < maxRetries` and read the new
		// count back so the cap decision is exact (no extra Steam call past the cap):
		//   - no row updated (sql.ErrNoRows): already resolved, gone, or already
		//     dead-lettered → drop silently (no false dead-letter alert).
		//   - new count == maxRetries: cap reached → dead-letter (stop re-queuing).
		//   - otherwise: re-queue for the next tick.
		var newCount int
		scanErr := w.db.QueryRow(ctx,
			`UPDATE achievement_retry_queue
			 SET retry_count = retry_count + 1, last_error = ?, next_retry_at = ?
			 WHERE player_id = ? AND achievement_id = ? AND resolved_at IS NULL AND retry_count < ?
			 RETURNING retry_count`,
			err.Error(), nextRetry.Format(time.RFC3339), playerID, achievementID, w.maxRetries,
		).Scan(&newCount)
		if errors.Is(scanErr, sql.ErrNoRows) {
			return false
		}
		if scanErr != nil {
			log.Error().Err(scanErr).
				Str("player_id", playerID).Str("achievement_id", achievementID).
				Msg("achievement worker: update retry queue on failure, re-queuing")
			return true
		}
		if newCount >= w.maxRetries {
			// Dead-letter: the row stays resolved_at IS NULL with retry_count ==
			// maxRetries (terminal, excluded from boot reload). Error level so a
			// permanently-stuck sync surfaces for manual intervention.
			log.Error().Err(err).
				Str("player_id", playerID).Str("achievement_id", achievementID).
				Int("retry_count", newCount).
				Msg("achievement worker: dead-lettered after max retries")
			return false
		}
		log.Warn().Err(err).
			Str("player_id", playerID).Str("achievement_id", achievementID).
			Int("retry_count", newCount).
			Msg("achievement worker: steam retry failed, re-queuing")
		return true
	}

	if _, err := w.db.Exec(ctx,
		"UPDATE player_achievements SET steam_synced = 1 WHERE player_id = ? AND achievement_id = ?",
		playerID, achievementID,
	); err != nil {
		log.Error().Err(err).Msg("achievement worker: update player_achievements steam_synced")
	}

	if _, err := w.db.Exec(ctx,
		"UPDATE achievement_retry_queue SET resolved_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE player_id = ? AND achievement_id = ? AND resolved_at IS NULL",
		playerID, achievementID,
	); err != nil {
		log.Error().Err(err).Msg("achievement worker: update resolved_at")
	}

	log.Info().
		Str("player_id", playerID).
		Str("achievement_id", achievementID).
		Msg("achievement worker: retry succeeded")
	return false
}
