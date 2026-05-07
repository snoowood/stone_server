package cache

import (
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

func NewClient(redisURL string) (*redis.Client, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	// Close idle connections after 5 minutes so memory returns to baseline
	// after load ends; don't pre-open connections before they are needed.
	opt.ConnMaxIdleTime = 5 * time.Minute
	opt.MinIdleConns = 0
	return redis.NewClient(opt), nil
}
