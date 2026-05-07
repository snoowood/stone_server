package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	jwtIssuer = "stone-server"
	jwtExpiry = 24 * time.Hour
)

// Claims holds the JWT payload.
type Claims struct {
	PlayerID string `json:"player_id"`
	jwt.RegisteredClaims
}

// ParsePrivateKey decodes a PEM-encoded RSA private key.
// Handles both PKCS1 and PKCS8 formats, and literal \n in env strings.
func ParsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("failed to decode PEM block from JWT_PRIVATE_KEY")
	}

	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS1 private key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		raw, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS8 private key: %w", err)
		}
		key, ok := raw.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("JWT_PRIVATE_KEY is not an RSA key")
		}
		return key, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type: %s", block.Type)
	}
}

// NewToken creates a signed RS256 JWT for the given player and jti.
// Returns the token string, its expiry time, and any error.
func NewToken(privKey *rsa.PrivateKey, playerID, jti string) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(jwtExpiry)

	claims := Claims{
		PlayerID: playerID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   playerID,
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenStr, err := token.SignedString(privKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign jwt: %w", err)
	}
	return tokenStr, expiresAt, nil
}
