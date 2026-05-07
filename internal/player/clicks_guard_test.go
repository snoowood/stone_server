package player

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gensdeis/stone-server/pkg/kvstore"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- batchExists / registerBatch ---

func TestBatchExists_NotRegistered(t *testing.T) {
	kv := kvstore.NewMemStore()
	if batchExists(context.Background(), kv, "p1", "batch-a") {
		t.Fatal("unregistered batch_id should not exist")
	}
}

func TestBatchExists_AfterRegister(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	registerBatch(ctx, kv, "p1", "batch-a")
	if !batchExists(ctx, kv, "p1", "batch-a") {
		t.Fatal("registered batch_id should exist")
	}
}

func TestRegisterBatch_FirstSucceeds(t *testing.T) {
	kv := kvstore.NewMemStore()
	if !registerBatch(context.Background(), kv, "p1", "batch-a") {
		t.Fatal("first registration should succeed")
	}
}

func TestRegisterBatch_DuplicateFails(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	registerBatch(ctx, kv, "p1", "batch-a")
	if registerBatch(ctx, kv, "p1", "batch-a") {
		t.Fatal("duplicate registration should fail")
	}
}

func TestRegisterBatch_DifferentPlayerAllowed(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	registerBatch(ctx, kv, "p1", "batch-a")
	if !registerBatch(ctx, kv, "p2", "batch-a") {
		t.Fatal("same batch_id for different player should be allowed")
	}
}

func TestRegisterBatch_AfterTTLAllowed(t *testing.T) {
	t.Skip("TTL expiry cannot be tested without time manipulation in MemStore")
}

// --- checkTooFrequent ---

func TestCheckTooFrequent_FirstAllowed(t *testing.T) {
	kv := kvstore.NewMemStore()
	if !checkTooFrequent(context.Background(), kv, "p1") {
		t.Fatal("first click should be allowed")
	}
}

func TestCheckTooFrequent_ImmediateRejected(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	checkTooFrequent(ctx, kv, "p1")
	if checkTooFrequent(ctx, kv, "p1") {
		t.Fatal("second click within 1s should be rejected")
	}
}

func TestCheckTooFrequent_AfterIntervalAllowed(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	past := time.Now().Add(-2 * time.Second).UnixNano()
	kv.Set(ctx, "click:last:p1", strconv.FormatInt(past, 10), lastTTL)
	if !checkTooFrequent(ctx, kv, "p1") {
		t.Fatal("click after 1s interval should be allowed")
	}
}

// --- checkHourlyRate ---

func TestCheckHourlyRate_UnderLimitAllowed(t *testing.T) {
	kv := kvstore.NewMemStore()
	if !checkHourlyRate(context.Background(), kv, "p1", 300) {
		t.Fatal("request well under hourly limit should be allowed")
	}
}

func TestCheckHourlyRate_AtLimitAllowed(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	kv.Set(ctx, "click:hourly:p1", "2700", time.Hour)
	if !checkHourlyRate(ctx, kv, "p1", 300) {
		t.Fatal("request that exactly reaches limit (3000) should be allowed")
	}
}

func TestCheckHourlyRate_ExceedRejected(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	kv.Set(ctx, "click:hourly:p1", "2800", time.Hour)
	if checkHourlyRate(ctx, kv, "p1", 201) {
		t.Fatal("request exceeding hourly limit should be rejected")
	}
}

func TestCheckHourlyRate_AfterResetAllowed(t *testing.T) {
	t.Skip("TTL expiry cannot be tested without time manipulation in MemStore")
}

// TestCheckHourlyRate_FailClosedOnCorruptValue verifies that a non-integer KV
// value (which would happen if the store is corrupted) is treated as fail-closed,
// preventing rate-limit bypass.
func TestCheckHourlyRate_FailClosedOnCorruptValue(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	kv.Set(ctx, "click:hourly:p1", "not-a-number", time.Hour)
	if checkHourlyRate(ctx, kv, "p1", 1) {
		t.Fatal("corrupt KV value must fail closed (not allow bypass)")
	}
}

// --- combination: order of checks via handler ---

// postClicksRequest calls PostClicks via httptest without a real DB.
// Guards run before the DB; if a guard rejects, DB is never reached.
func postClicksRequest(h *Handler, playerID, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("player_id", playerID)
	c.Request = httptest.NewRequest(http.MethodPost, "/player/clicks", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.PostClicks(c)
	return w
}

func TestPostClicks_InvalidCount(t *testing.T) {
	kv := kvstore.NewMemStore()
	h := &Handler{kv: kv}

	cases := []struct {
		body string
		code string
	}{
		{`{"batch_id":"b1","count":0}`, "INVALID_COUNT"},
		{`{"batch_id":"b1","count":301}`, "INVALID_COUNT"},
		{`{"count":10}`, "INVALID_REQUEST"}, // missing batch_id
	}
	for _, tc := range cases {
		w := postClicksRequest(h, "p1", tc.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s: want 400, got %d", tc.body, w.Code)
		}
		if !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("body %s: want %s in body: %s", tc.body, tc.code, w.Body.String())
		}
	}
}

func TestPostClicks_DuplicateBatch(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	h := &Handler{kv: kv}

	kv.Set(ctx, "click:batch:p1:batch-dup", "1", batchTTL)

	w := postClicksRequest(h, "p1", `{"batch_id":"batch-dup","count":10}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "DUPLICATE_BATCH") {
		t.Fatalf("want DUPLICATE_BATCH: %s", w.Body.String())
	}
}

func TestPostClicks_TooFrequent(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	h := &Handler{kv: kv}

	kv.Set(ctx, "click:last:p1", strconv.FormatInt(time.Now().UnixNano(), 10), lastTTL)

	w := postClicksRequest(h, "p1", `{"batch_id":"batch-freq","count":10}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "TOO_FREQUENT") {
		t.Fatalf("want TOO_FREQUENT: %s", w.Body.String())
	}
}

func TestPostClicks_RateExceeded(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	h := &Handler{kv: kv}

	kv.Set(ctx, "click:hourly:p1", "3000", time.Hour)

	w := postClicksRequest(h, "p1", `{"batch_id":"batch-rate","count":1}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "RATE_EXCEEDED") {
		t.Fatalf("want RATE_EXCEEDED: %s", w.Body.String())
	}
}

// TestPostClicks_DuplicateBatchBeforeTooFrequent verifies that an already-registered
// batch_id returns 409 even when the request would also be TOO_FREQUENT.
func TestPostClicks_DuplicateBatchBeforeTooFrequent(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	h := &Handler{kv: kv}

	kv.Set(ctx, "click:batch:p1:batch-x", "1", batchTTL)
	kv.Set(ctx, "click:last:p1", strconv.FormatInt(time.Now().UnixNano(), 10), lastTTL)

	w := postClicksRequest(h, "p1", `{"batch_id":"batch-x","count":10}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409 (DUPLICATE_BATCH before TOO_FREQUENT), got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "DUPLICATE_BATCH") {
		t.Fatalf("want DUPLICATE_BATCH: %s", w.Body.String())
	}
}

// TestPostClicks_TooFrequentDoesNotConsumeBatch verifies that a TOO_FREQUENT rejection
// does not register the batch_id, allowing retry with the same batch_id after rate clears.
func TestPostClicks_TooFrequentDoesNotConsumeBatch(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	h := &Handler{kv: kv}

	kv.Set(ctx, "click:last:p1", strconv.FormatInt(time.Now().UnixNano(), 10), lastTTL)

	w := postClicksRequest(h, "p1", `{"batch_id":"batch-new","count":10}`)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "TOO_FREQUENT") {
		t.Fatalf("want 429 TOO_FREQUENT, got %d: %s", w.Code, w.Body.String())
	}
	exists, _ := kv.Exists(ctx, "click:batch:p1:batch-new")
	if exists {
		t.Fatal("batch_id must not be registered when rejected by TOO_FREQUENT")
	}
}

// TestPostClicks_RateExceededDoesNotConsumeBatch verifies that a RATE_EXCEEDED rejection
// does not register the batch_id, allowing retry with the same batch_id after rate clears.
func TestPostClicks_RateExceededDoesNotConsumeBatch(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	h := &Handler{kv: kv}

	kv.Set(ctx, "click:hourly:p1", "3000", time.Hour)

	w := postClicksRequest(h, "p1", `{"batch_id":"batch-new","count":1}`)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "RATE_EXCEEDED") {
		t.Fatalf("want 429 RATE_EXCEEDED, got %d: %s", w.Code, w.Body.String())
	}
	exists, _ := kv.Exists(ctx, "click:batch:p1:batch-new")
	if exists {
		t.Fatal("batch_id must not be registered when rejected by RATE_EXCEEDED")
	}
}

// TestPostClicks_TooFrequentTakesPriorityOverRateExceeded verifies check order:
// TOO_FREQUENT is evaluated before RATE_EXCEEDED.
func TestPostClicks_TooFrequentTakesPriorityOverRateExceeded(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	h := &Handler{kv: kv}

	kv.Set(ctx, "click:last:p1", strconv.FormatInt(time.Now().UnixNano(), 10), lastTTL)
	kv.Set(ctx, "click:hourly:p1", "3000", time.Hour)

	w := postClicksRequest(h, "p1", `{"batch_id":"batch-y","count":1}`)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "TOO_FREQUENT") {
		t.Fatalf("want TOO_FREQUENT (before RATE_EXCEEDED): %s", w.Body.String())
	}
}
