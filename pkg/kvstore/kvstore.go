package kvstore

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("key not found")

// KVStore is a minimal key-value store with TTL support, replacing Redis for lightweight use.
type KVStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, val string, ttl time.Duration) error
	SetNX(ctx context.Context, key, val string, ttl time.Duration) (bool, error)
	Del(ctx context.Context, keys ...string) error
	Exists(ctx context.Context, key string) (bool, error)
	IncrBy(ctx context.Context, key string, n int64) (int64, error)
	Expire(ctx context.Context, key string, ttl time.Duration) error
	LPush(ctx context.Context, key, val string) error
	RPush(ctx context.Context, key, val string) error
	RPop(ctx context.Context, key string) (string, error)
	Ping(ctx context.Context) error
}
