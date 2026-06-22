package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// LOG-7: the request line's level must branch by status (5xx→error, 4xx→warn,
// else→info) and carry reject_code when a handler/middleware set one.
func TestRequestLogger_LevelByStatusAndRejectCode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var buf bytes.Buffer
	origLogger := log.Logger
	origLevel := zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf)
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	t.Cleanup(func() {
		log.Logger = origLogger
		zerolog.SetGlobalLevel(origLevel)
	})

	r := gin.New()
	r.Use(RequestLogger())
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/bad", func(c *gin.Context) {
		c.Set("reject_code", "RATE_LIMIT_EXCEEDED")
		c.Status(http.StatusTooManyRequests)
	})
	r.GET("/boom", func(c *gin.Context) { c.Status(http.StatusInternalServerError) })

	cases := []struct {
		path       string
		wantLevel  string
		wantReject string
	}{
		{"/ok", "info", ""},
		{"/bad", "warn", "RATE_LIMIT_EXCEEDED"},
		{"/boom", "error", ""},
	}
	for _, tc := range cases {
		buf.Reset()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))

		line := findRequestLine(t, &buf)
		if line["level"] != tc.wantLevel {
			t.Errorf("%s: level = %v, want %s", tc.path, line["level"], tc.wantLevel)
		}
		if tc.wantReject == "" {
			if v, ok := line["reject_code"]; ok {
				t.Errorf("%s: unexpected reject_code %v", tc.path, v)
			}
		} else if line["reject_code"] != tc.wantReject {
			t.Errorf("%s: reject_code = %v, want %s", tc.path, line["reject_code"], tc.wantReject)
		}
	}
}

func findRequestLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var found map[string]any
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		if m["message"] == "request" {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("no request log line in output: %q", buf.String())
	}
	return found
}
