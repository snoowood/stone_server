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

type workerDB interface {
	QueryRow(ctx context.Context, query string, args ...any) store.Row
	Exec(ctx context.Context, query string, args ...any) (store.Result, error)
}

type Worker struct {
	db           workerDB
	kv           kvstore.KVStore
	steam        SteamAchievementClient
	tickInterval time.Duration
}

func NewWorker(db store.DB, kv kvstore.KVStore, steam SteamAchievementClient) *Worker {
	return &Worker{db: db, kv: kv, steam: steam, tickInterval: time.Minute}
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
		log.Warn().Err(err).
			Str("player_id", playerID).
			Str("achievement_id", achievementID).
			Msg("achievement worker: steam retry failed, re-queuing")

		nextRetry := time.Now().Add(w.tickInterval).UTC()
		if _, execErr := w.db.Exec(ctx,
			`UPDATE achievement_retry_queue
			 SET retry_count = retry_count + 1, last_error = ?, next_retry_at = ?
			 WHERE player_id = ? AND achievement_id = ? AND resolved_at IS NULL`,
			err.Error(), nextRetry.Format(time.RFC3339), playerID, achievementID,
		); execErr != nil {
			log.Error().Err(execErr).Msg("achievement worker: update retry queue on failure")
		}
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
