package kvstore

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore wraps *redis.Client to implement KVStore (dormant Redis path).
type RedisStore struct{ rdb *redis.Client }

func NewRedisStore(rdb *redis.Client) KVStore { return &RedisStore{rdb} }

func (r *RedisStore) Get(ctx context.Context, key string) (string, error) {
	val, err := r.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", ErrNotFound
	}
	return val, err
}

func (r *RedisStore) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	return r.rdb.Set(ctx, key, val, ttl).Err()
}

func (r *RedisStore) SetNX(ctx context.Context, key, val string, ttl time.Duration) (bool, error) {
	return r.rdb.SetNX(ctx, key, val, ttl).Result()
}

func (r *RedisStore) Del(ctx context.Context, keys ...string) error {
	return r.rdb.Del(ctx, keys...).Err()
}

func (r *RedisStore) Exists(ctx context.Context, key string) (bool, error) {
	n, err := r.rdb.Exists(ctx, key).Result()
	return n > 0, err
}

func (r *RedisStore) IncrBy(ctx context.Context, key string, n int64) (int64, error) {
	return r.rdb.IncrBy(ctx, key, n).Result()
}

func (r *RedisStore) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return r.rdb.Expire(ctx, key, ttl).Err()
}

func (r *RedisStore) LPush(ctx context.Context, key, val string) error {
	return r.rdb.LPush(ctx, key, val).Err()
}

func (r *RedisStore) RPush(ctx context.Context, key, val string) error {
	return r.rdb.RPush(ctx, key, val).Err()
}

func (r *RedisStore) RPop(ctx context.Context, key string) (string, error) {
	val, err := r.rdb.RPop(ctx, key).Result()
	if err == redis.Nil {
		return "", ErrNotFound
	}
	return val, err
}

func (r *RedisStore) Ping(ctx context.Context) error {
	return r.rdb.Ping(ctx).Err()
}
