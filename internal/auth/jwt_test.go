package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// SEC-6 (stage 1): NewToken must issue both the `iss` and `aud` claims so that a later
// stage-2 deploy can enforce WithIssuer/WithAudience once old tokens have rotated out.
func TestNewToken_IssuesIssAndAud(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tokenStr, _, err := NewToken(priv, "player-1", "jti-1")
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	claims := &Claims{}
	if _, _, err := jwt.NewParser().ParseUnverified(tokenStr, claims); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if claims.Issuer != jwtIssuer {
		t.Errorf("iss = %q, want %q", claims.Issuer, jwtIssuer)
	}
	found := false
	for _, a := range claims.Audience {
		if a == jwtAudience {
			found = true
		}
	}
	if !found {
		t.Errorf("aud = %v, want to contain %q", claims.Audience, jwtAudience)
	}
}
