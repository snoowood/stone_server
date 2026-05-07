package health

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

const version = "1.0.0"

func Handler(db store.DB, kv kvstore.KVStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		var (
			dbStatus    = "ok"
			cacheStatus = "ok"
			wg          sync.WaitGroup
			mu          sync.Mutex
		)

		wg.Add(2)

		go func() {
			defer wg.Done()
			if err := db.Ping(ctx); err != nil {
				mu.Lock()
				dbStatus = "error"
				mu.Unlock()
			}
		}()

		go func() {
			defer wg.Done()
			if err := kv.Ping(ctx); err != nil {
				mu.Lock()
				cacheStatus = "error"
				mu.Unlock()
			}
		}()

		wg.Wait()

		httpStatus := http.StatusOK
		status := "ok"
		if dbStatus == "error" || cacheStatus == "error" {
			httpStatus = http.StatusServiceUnavailable
			status = "degraded"
		}

		c.JSON(httpStatus, gin.H{
			"status":  status,
			"db":      dbStatus,
			"cache":   cacheStatus,
			"version": version,
		})
	}
}
