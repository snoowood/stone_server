package timeguard

import (
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const driftWarningThreshold = 300 * time.Second

type syncRequest struct {
	ClientTimestamp time.Time `json:"client_timestamp" binding:"required"`
}

type syncResponse struct {
	ServerTime   time.Time `json:"server_time"`
	DriftSeconds int64     `json:"drift_seconds"`
	Warning      bool      `json:"warning"`
}

// Handler handles POST /api/v1/time/sync.
func Handler(c *gin.Context) {
	var req syncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "client_timestamp is required (RFC3339)", "code": "INVALID_REQUEST"})
		return
	}

	serverTime := time.Now().UTC()
	drift := time.Duration(math.Abs(float64(serverTime.Sub(req.ClientTimestamp))))
	driftSeconds := int64(drift.Seconds())

	c.JSON(http.StatusOK, syncResponse{
		ServerTime:   serverTime,
		DriftSeconds: driftSeconds,
		Warning:      drift > driftWarningThreshold,
	})
}
