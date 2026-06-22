package middleware

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"
)

func newRecoveryRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestLogger()) // provides the request-bound logger Recovery logs through
	r.Use(Recovery())
	return r
}

// LOG-2: a panic in a handler must be recovered into a structured 500 JSON
// response (not crash the process or emit a plaintext stack to the client).
func TestRecovery_PanicReturns500JSON(t *testing.T) {
	r := newRecoveryRouter()
	r.GET("/boom", func(c *gin.Context) { panic("kaboom") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d", w.Code)
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v (body=%s)", err, w.Body.String())
	}
	if body.Code != "INTERNAL_ERROR" {
		t.Errorf("code: want INTERNAL_ERROR, got %q", body.Code)
	}
}

// If the handler already committed a status/body before panicking, Recovery must
// NOT overwrite it with a 500 JSON (would corrupt a partially-sent response).
func TestRecovery_PanicAfterPartialWrite_NoOverwrite(t *testing.T) {
	r := newRecoveryRouter()
	r.GET("/partial", func(c *gin.Context) {
		c.String(http.StatusOK, "partial")
		panic("after write")
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/partial", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200 (not overwritten), got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "INTERNAL_ERROR") {
		t.Errorf("must not overwrite committed body with 500 JSON, got %q", w.Body.String())
	}
}

// A broken-pipe / connection-reset panic means the client is gone — Recovery must
// write NO status to the dead socket (parity with gin.Recovery), not even a 500.
func TestRecovery_BrokenPipe_NoResponseWritten(t *testing.T) {
	r := newRecoveryRouter()
	r.GET("/broken", func(c *gin.Context) {
		panic(&net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("write", syscall.EPIPE)})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/broken", nil))

	// httptest.ResponseRecorder defaults Code to 200 when nothing is written —
	// so a 500 (or any explicit status) would be visible here. None must appear.
	if w.Code != http.StatusOK {
		t.Errorf("broken pipe must not write a status to the dead socket, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("broken pipe must not write a body, got %q", w.Body.String())
	}
}

// Classifier parity with gin v1.12.0: EPIPE / ECONNRESET / http.ErrAbortHandler
// (including wrapped chains) are broken-pipe; ordinary panics are not.
func TestIsBrokenPipe(t *testing.T) {
	cases := []struct {
		name string
		rec  any
		want bool
	}{
		{"EPIPE via OpError/SyscallError", &net.OpError{Op: "write", Err: os.NewSyscallError("write", syscall.EPIPE)}, true},
		{"ECONNRESET via OpError/SyscallError", &net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}, true},
		{"wrapped EPIPE", fmt.Errorf("write tcp: %w", syscall.EPIPE), true},
		{"ErrAbortHandler", http.ErrAbortHandler, true},
		{"wrapped ErrAbortHandler", fmt.Errorf("aborted: %w", http.ErrAbortHandler), true},
		{"string panic", "boom", false},
		{"plain error", errors.New("some error"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBrokenPipe(tc.rec); got != tc.want {
				t.Errorf("isBrokenPipe(%v) = %v, want %v", tc.rec, got, tc.want)
			}
		})
	}
}
