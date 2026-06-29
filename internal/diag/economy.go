// Package diag provides read-only, development-only diagnostic endpoints.
// Routes here are registered only when APP_ENV == development (see cmd/server/main.go),
// so they are absent in production by construction.
package diag

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/gensdeis/stone-server/pkg/store"
)

// reconcileEpsilon is the absolute float tolerance for the balance identity / continuity checks.
const reconcileEpsilon = 1e-6

// reconcileRelEpsilon scales the row-identity tolerance by balance magnitude: the residual of
// re-evaluating the identity in float64 grows with the operands (f64 ULP near the NUMERIC(12,2)
// ceiling exceeds the absolute epsilon), so a tiny relative term avoids false positives on very
// large valid balances while staying far below a 1-cent discrepancy at any realistic balance.
const reconcileRelEpsilon = 1e-12

// accrualMarginSecs is added (×rate) to the per-interval accrual ceiling to absorb the
// second-resolution rounding of log timestamps (each floored timestamp can differ from
// the real accrual elapsed by up to ~1s on either side).
const accrualMarginSecs = 2.0

// eventAudit is one economy-mutating event (a gacha pull or a vow) as recorded in
// gacha_logs / vow_logs.
type eventAudit struct {
	Kind          string    `json:"kind"` // "gacha" | "vow"
	ID            string    `json:"id"`
	At            time.Time `json:"at"`
	CostPoints    float64   `json:"cost_points"`
	AccruedPts    float64   `json:"accrued_pts"`
	BalanceBefore float64   `json:"balance_before"`
	BalanceAfter  float64   `json:"balance_after"`
}

// reconcileAnomaly flags a row that violates the per-event balance identity, or a pair
// of consecutive events whose balances cannot be explained by passive accrual.
type reconcileAnomaly struct {
	Type      string  `json:"type"` // "row_identity" | "negative_continuity" | "excess_accrual"
	EventKind string  `json:"event_kind"`
	EventID   string  `json:"event_id"`
	At        string  `json:"at"`
	Delta     float64 `json:"delta"`
	Detail    string  `json:"detail"`
}

// economyAudit is the per-player reconciliation report.
type economyAudit struct {
	PlayerID            string             `json:"player_id"`
	CurrentBalance      float64            `json:"current_balance"`
	EnlightenmentRate   float64            `json:"enlightenment_rate"`
	LastSyncAt          *time.Time         `json:"last_sync_at"`
	GachaPulls          int                `json:"gacha_pulls"`
	VowCount            int                `json:"vow_count"`
	TotalEvents         int                `json:"total_events"`
	TotalAccrued        float64            `json:"total_accrued"`
	TotalCost           float64            `json:"total_cost"`
	NetLogged           float64            `json:"net_logged"`            // total_accrued - total_cost
	FirstBalanceBefore  *float64           `json:"first_balance_before"`  // nil if no events
	LastBalanceAfter    *float64           `json:"last_balance_after"`    // nil if no events
	UnloggedAccrual     float64            `json:"unlogged_accrual"`      // banked via /player/sync between events
	MaxPlausibleAccrual float64            `json:"max_plausible_accrual"` // Σ per-gap accrual ceiling ((elapsed+margin)×rate); unlogged_accrual cannot exceed it
	SameSecondEvents    bool               `json:"same_second_events"`    // true if any events share a second (order reconstructed by balance)
	Reconciled          bool               `json:"reconciled"`
	Anomalies           []reconcileAnomaly `json:"anomalies"`
	Notes               []string           `json:"notes"`
}

// EconomyHandler handles GET /api/v1/internal/diag/economy?player_id=X.
//
// It reconstructs a player's economy ledger from gacha_logs + vow_logs and reports
// whether the logged balances are internally consistent.
//
// An absolute reconcile (current_balance == initial + Σaccrued − Σcost) is intentionally
// NOT attempted: POST /player/sync banks passive accrual into enlightenment_pts with no
// economy-log row, so the live balance legitimately exceeds what the logs sum to. Instead
// two checks run:
//
//   - row_identity: balance_after == balance_before + accrued_pts − cost_points. The
//     server derives accrued := balance_after − balance_before + cost, so this holds by
//     construction for every honestly-written row — it therefore detects only column-level
//     corruption / post-hoc tampering, NOT upstream accrual/cost logic bugs.
//   - continuity (the economically meaningful check): between consecutive events, a balance
//     drop that cannot be explained (next.balance_before < prev.balance_after) is flagged
//     negative_continuity, and a balance jump exceeding the maximum passive accrual
//     (elapsed × enlightenment_rate, which is immutable per player) is flagged excess_accrual.
//
// Log timestamps are second-resolution. Within a single second the wall-clock order is not
// stored, so a same-second transition is treated as consistent when EITHER ordering fits the
// accrual bound (a zero-cost debug vow can legitimately raise the balance) — only a pair no
// ordering can explain is flagged. Across seconds, time orders the events. A run of 3+ events
// sharing one second is checked pairwise in descending-balance order (a heuristic, surfaced
// via same_second_events).
func EconomyHandler(db store.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		playerID := c.Query("player_id")
		if playerID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "player_id is required", "code": "INVALID_REQUEST"})
			return
		}
		ctx := c.Request.Context()

		var (
			curBalance float64
			rate       float64
			lastSyncAt *time.Time
		)
		err := db.QueryRow(ctx, `
			SELECT enlightenment_pts, enlightenment_rate, last_sync_at
			FROM player_states
			WHERE player_id = ?
		`, playerID).Scan(&curBalance, &rate, store.ScanNullTime(&lastSyncAt))
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "player state not found", "code": "NOT_FOUND"})
			return
		}
		if err != nil {
			log.Error().Err(err).Str("player_id", playerID).Msg("diag economy: query player_states")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}

		// Merge both economy log tables into one event stream.
		// SQLite-dialect SQL ('?' placeholders + the existing schema); like every other
		// handler this is the SQLite-only path — a PostgreSQL cutover (R1) must rewrite
		// '?'→'$N' here too. Dev-only: this loads the player's full ledger by design (the
		// reconciliation must walk every event); it is not a hot-path query.
		rows, err := db.Query(ctx, `
			SELECT kind, id, at, cost_points, accrued_pts, balance_before, balance_after
			FROM (
				SELECT 'gacha' AS kind, id, pulled_at AS at, cost_points, accrued_pts, balance_before, balance_after
				FROM gacha_logs WHERE player_id = ?
				UNION ALL
				SELECT 'vow' AS kind, id, prayed_at AS at, cost_points, accrued_pts, balance_before, balance_after
				FROM vow_logs WHERE player_id = ?
			)
		`, playerID, playerID)
		if err != nil {
			log.Error().Err(err).Str("player_id", playerID).Msg("diag economy: query logs")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}
		defer rows.Close()

		var (
			events       []eventAudit
			totalAccrued float64
			totalCost    float64
			gachaPulls   int
			vowCount     int
		)
		for rows.Next() {
			var e eventAudit
			if err := rows.Scan(&e.Kind, &e.ID, store.ScanTime(&e.At), &e.CostPoints, &e.AccruedPts, &e.BalanceBefore, &e.BalanceAfter); err != nil {
				log.Error().Err(err).Str("player_id", playerID).Msg("diag economy: scan log row")
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
				return
			}
			events = append(events, e)
			totalAccrued += e.AccruedPts
			totalCost += e.CostPoints
			if e.Kind == "gacha" {
				gachaPulls++
			} else {
				vowCount++
			}
		}
		if err := rows.Err(); err != nil {
			log.Error().Err(err).Str("player_id", playerID).Msg("diag economy: iterate logs")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})
			return
		}

		// Reconstruct chronological order: by timestamp, then (within a second) by descending
		// balance — real same-second events decrease the balance monotonically, so this
		// recovers their sequence. ID is the final, stable tiebreak.
		sort.SliceStable(events, func(i, j int) bool {
			a, b := events[i], events[j]
			switch {
			case !a.At.Equal(b.At):
				return a.At.Before(b.At)
			case a.BalanceBefore != b.BalanceBefore:
				return a.BalanceBefore > b.BalanceBefore
			case a.BalanceAfter != b.BalanceAfter:
				return a.BalanceAfter > b.BalanceAfter
			default:
				return a.ID < b.ID
			}
		})

		anomalies := []reconcileAnomaly{}

		// (1) Per-row identity — order-independent.
		for _, e := range events {
			tol := reconcileEpsilon + reconcileRelEpsilon*math.Max(math.Abs(e.BalanceBefore), math.Abs(e.BalanceAfter))
			if d := e.BalanceAfter - (e.BalanceBefore + e.AccruedPts - e.CostPoints); math.Abs(d) > tol {
				anomalies = append(anomalies, reconcileAnomaly{
					Type:      "row_identity",
					EventKind: e.Kind,
					EventID:   e.ID,
					At:        e.At.UTC().Format(time.RFC3339),
					Delta:     d,
					Detail:    "balance_after != balance_before + accrued_pts - cost_points",
				})
			}
		}

		// (2) Inter-event continuity over the reconstructed order.
		var unloggedAccrual, maxPlausibleAccrual float64
		sameSecond := false
		inBounds := func(g, ceiling float64) bool {
			return g >= -reconcileEpsilon && g <= ceiling+reconcileEpsilon
		}
		for i := 1; i < len(events); i++ {
			prev, cur := events[i-1], events[i]
			elapsed := cur.At.Sub(prev.At).Seconds()
			if elapsed < 0 {
				elapsed = 0
			}
			ceiling := (elapsed + accrualMarginSecs) * rate
			gap := cur.BalanceBefore - prev.BalanceAfter

			if elapsed == 0 {
				// Same second: sub-second order is not stored and either direction is
				// physically possible, so the pair is consistent if EITHER ordering fits the
				// bound. Flag only when no ordering explains it. Sub-second accrual is
				// negligible, so it is not added to unlogged_accrual.
				sameSecond = true
				revGap := prev.BalanceBefore - cur.BalanceAfter
				if inBounds(gap, ceiling) || inBounds(revGap, ceiling) {
					continue
				}
			} else {
				// Accumulate the per-gap ceiling (not bare elapsed×rate) so unlogged_accrual,
				// whose accepted gaps are each ≤ ceiling, can never exceed max_plausible_accrual.
				maxPlausibleAccrual += ceiling
				if inBounds(gap, ceiling) {
					unloggedAccrual += gap // 0 ≤ gap ≤ ceiling: passive accrual banked between events
					continue
				}
			}

			// No ordering explains this transition — classify by the gap.
			if gap < -reconcileEpsilon {
				anomalies = append(anomalies, reconcileAnomaly{
					Type:      "negative_continuity",
					EventKind: cur.Kind,
					EventID:   cur.ID,
					At:        cur.At.UTC().Format(time.RFC3339),
					Delta:     gap,
					Detail:    "balance_before is below the previous event's balance_after (unexplained drain)",
				})
			} else {
				anomalies = append(anomalies, reconcileAnomaly{
					Type:      "excess_accrual",
					EventKind: cur.Kind,
					EventID:   cur.ID,
					At:        cur.At.UTC().Format(time.RFC3339),
					Delta:     gap - ceiling,
					Detail:    "balance jump exceeds the maximum passive accrual (elapsed * enlightenment_rate) for the interval",
				})
			}
		}

		var firstBefore, lastAfter *float64
		if len(events) > 0 {
			fb := events[0].BalanceBefore
			la := events[len(events)-1].BalanceAfter
			firstBefore = &fb
			lastAfter = &la
		}

		c.JSON(http.StatusOK, economyAudit{
			PlayerID:            playerID,
			CurrentBalance:      curBalance,
			EnlightenmentRate:   rate,
			LastSyncAt:          lastSyncAt,
			GachaPulls:          gachaPulls,
			VowCount:            vowCount,
			TotalEvents:         len(events),
			TotalAccrued:        totalAccrued,
			TotalCost:           totalCost,
			NetLogged:           totalAccrued - totalCost,
			FirstBalanceBefore:  firstBefore,
			LastBalanceAfter:    lastAfter,
			UnloggedAccrual:     unloggedAccrual,
			MaxPlausibleAccrual: maxPlausibleAccrual,
			SameSecondEvents:    sameSecond,
			Reconciled:          len(anomalies) == 0,
			Anomalies:           anomalies,
			Notes: []string{
				"Absolute reconcile (current_balance == initial + sum(accrued) - sum(cost)) is not attempted: POST /player/sync banks passive accrual with no log row, so the live balance legitimately exceeds the logged sum.",
				"row_identity (balance_after == balance_before + accrued_pts - cost_points) detects only column-level corruption — accrued_pts is server-derived to satisfy this identity, so it cannot catch upstream accrual/cost logic bugs.",
				"continuity is the economic check: negative_continuity = a balance drop with no explanation; excess_accrual = a jump above elapsed * enlightenment_rate. Compare unlogged_accrual against max_plausible_accrual.",
				"Log timestamps are second-resolution; a same-second pair is flagged only when no ordering fits the accrual bound (either direction is allowed, since a zero-cost debug vow can raise the balance). same_second_events flags when this applied.",
			},
		})
	}
}
