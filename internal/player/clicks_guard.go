package player

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gensdeis/stone-server/pkg/kvstore"
)

const (
	batchTTL    = 5 * time.Minute
	lastTTL     = 10 * time.Second
	hourlyTTL   = time.Hour
	maxHourly   = 3000
	minInterval = time.Second
)

func batchExists(ctx context.Context, kv kvstore.KVStore, playerID, batchID string) bool {
	key := fmt.Sprintf("click:batch:%s:%s", playerID, batchID)
	ok, _ := kv.Exists(ctx, key)
	return ok
}

func registerBatch(ctx context.Context, kv kvstore.KVStore, playerID, batchID string) bool {
	key := fmt.Sprintf("click:batch:%s:%s", playerID, batchID)
	ok, _ := kv.SetNX(ctx, key, "1", batchTTL)
	return ok
}

func checkTooFrequent(ctx context.Context, kv kvstore.KVStore, playerID string) bool {
	key := fmt.Sprintf("click:last:%s", playerID)
	now := time.Now().UnixNano()
	val, err := kv.Get(ctx, key)
	if err == nil {
		last, parseErr := strconv.ParseInt(val, 10, 64)
		if parseErr == nil && time.Duration(now-last) < minInterval {
			return false
		}
	}
	kv.Set(ctx, key, strconv.FormatInt(now, 10), lastTTL)
	return true
}

func checkHourlyRate(ctx context.Context, kv kvstore.KVStore, playerID string, count int) bool {
	key := fmt.Sprintf("click:hourly:%s", playerID)
	val, err := kv.Get(ctx, key)
	if err != nil {
		if errors.Is(err, kvstore.ErrNotFound) {
			return true
		}
		return false // store error → fail closed (prevent rate-limit bypass during KV outage)
	}
	cur, err := strconv.Atoi(val)
	if err != nil {
		return false
	}
	return cur+count <= maxHourly
}

func incrHourly(ctx context.Context, kv kvstore.KVStore, playerID string, count int) {
	key := fmt.Sprintf("click:hourly:%s", playerID)
	newVal, _ := kv.IncrBy(ctx, key, int64(count))
	if newVal == int64(count) {
		kv.Expire(ctx, key, hourlyTTL)
	}
}
