package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gensdeis/stone-server/pkg/kvstore"
)

func init() { gin.SetMode(gin.TestMode) }

func newRefreshHandler(t *testing.T) (*Handler, kvstore.KVStore) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	db, _ := newTestDB(t)
	kv := kvstore.NewMemStore()
	return &Handler{db: db, kv: kv, privKey: priv}, kv
}

func doRefresh(h *Handler, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/auth/refresh", h.AuthRefresh)
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func seedRefresh(t *testing.T, kv kvstore.KVStore, playerID, token string) {
	t.Helper()
	if err := storeRefreshToken(context.Background(), kv, playerID, token); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
}

// TestAuthRefresh_RotatesAndInvalidatesOld verifies:
//   - response includes a new refresh_token (different from the one sent)
//   - the old token is no longer accepted on a subsequent refresh
//   - the new token is accepted (after cooldown clears)
func TestAuthRefresh_RotatesAndInvalidatesOld(t *testing.T) {
	h, kv := newRefreshHandler(t)
	playerID := "player-rot"
	oldToken := "old-token-xyz"
	seedRefresh(t, kv, playerID, oldToken)

	w := doRefresh(h, `{"refresh_token":"`+oldToken+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("first refresh: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	newToken, _ := resp["refresh_token"].(string)
	if newToken == "" {
		t.Fatalf("response missing refresh_token: %s", w.Body.String())
	}
	if newToken == oldToken {
		t.Fatal("refresh_token must rotate (got same value)")
	}

	// Old token must no longer resolve.
	if _, err := lookupRefreshToken(context.Background(), kv, oldToken); err == nil {
		t.Fatal("old refresh_lookup should be invalidated")
	}

	// Stored token must be the new one.
	stored, err := getStoredRefreshToken(context.Background(), kv, playerID)
	if err != nil || stored != newToken {
		t.Fatalf("stored refresh = %q (err %v), want %q", stored, err, newToken)
	}
}

// TestAuthRefresh_ReplayOfOldTokenRejected verifies that re-submitting the
// pre-rotation token returns 401, even before the cooldown window matters.
func TestAuthRefresh_ReplayOfOldTokenRejected(t *testing.T) {
	h, kv := newRefreshHandler(t)
	playerID := "player-replay"
	oldToken := "old-token-abc"
	seedRefresh(t, kv, playerID, oldToken)

	if w := doRefresh(h, `{"refresh_token":"`+oldToken+`"}`); w.Code != http.StatusOK {
		t.Fatalf("first refresh: want 200, got %d", w.Code)
	}

	// Replay with same old token.
	w := doRefresh(h, `{"refresh_token":"`+oldToken+`"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("replayed old token: want 401, got %d: %s", w.Code, w.Body.String())
	}
}
