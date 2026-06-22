package middleware

import (
	"errors"
	"net/http"
	"runtime/debug"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

// Recovery recovers from panics in downstream handlers and logs the panic value
// + stack via zerolog, using the request-bound logger so request_id (and
// player_id, when authenticated) join the existing "request" line. This replaces
// gin.Recovery(), whose plaintext stderr stack dump both pollutes the structured
// JSON log stream and cannot be correlated with the originating request.
//
// Must be registered AFTER RequestLogger() so the bound logger is already in the
// request context when the deferred recover fires.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}

			// A broken pipe / connection reset means the client disconnected
			// mid-response — not a server fault. Mirror gin.Recovery: log it
			// quietly (no alarming stack) and do NOT try to write a status to a
			// dead socket. Debug level keeps these benign, high-volume events out
			// of prod (info) logs.
			if isBrokenPipe(rec) {
				zerolog.Ctx(c.Request.Context()).Debug().
					Interface("err", rec).
					Str("method", c.Request.Method).
					Str("path", c.Request.URL.Path).
					Msg("connection broken (client disconnected)")
				if err, ok := rec.(error); ok {
					_ = c.Error(err)
				}
				c.Abort()
				return
			}

			zerolog.Ctx(c.Request.Context()).Error().
				Interface("panic", rec).
				Str("method", c.Request.Method).
				Str("path", c.Request.URL.Path).
				Str("stack", string(debug.Stack())).
				Msg("panic recovered")

			// If the handler already started writing the response, we can only
			// abort; otherwise emit a structured 500 matching the rest of the API.
			if c.Writer.Written() {
				c.Abort()
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "internal error",
				"code":  "INTERNAL_ERROR",
			})
		}()
		c.Next()
	}
}

// isBrokenPipe reports whether the recovered panic value is a dead-connection
// error (client disconnected) or an explicit handler abort. Mirrors gin v1.12.0's
// CustomRecoveryWithWriter check exactly (errors.Is over the full chain) so we
// don't treat a dead connection as a server panic.
func isBrokenPipe(rec any) bool {
	err, ok := rec.(error)
	if !ok {
		return false
	}
	return errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, http.ErrAbortHandler)
}
