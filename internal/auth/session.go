package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gensdeis/stone-server/pkg/kvstore"
)

const (
	sessionTTL     = 24 * time.Hour
	refreshTTL     = 30 * 24 * time.Hour
	ticketTTL      = time.Hour
	sessionLockTTL = 5 * time.Second
)

// ErrSessionInFlight is returned when a concurrent session enforcement is in progress
// for the same player. Callers should respond with 409 CONFLICT.
var ErrSessionInFlight = errors.New("session enforcement in flight")

// enforceSingleSession removes any existing session for playerID and registers jti.
// A short-lived per-player lock makes the read-old / del-old / set-new sequence atomic
// across concurrent logins, preventing two JWTs from coexisting.
func enforceSingleSession(ctx context.Context, kv kvstore.KVStore, playerID, jti string) error {
	lockKey := fmt.Sprintf("session:lock:%s", playerID)
	locked, err := kv.SetNX(ctx, lockKey, "1", sessionLockTTL)
	if err != nil {
		return err
	}
	if !locked {
		return ErrSessionInFlight
	}
	defer kv.Del(context.Background(), lockKey)

	currentKey := fmt.Sprintf("session:current:%s", playerID)

	oldJTI, err := kv.Get(ctx, currentKey)
	if err == nil && oldJTI != "" {
		kv.Del(ctx, "session:"+oldJTI)
	}

	if err := kv.Set(ctx, "session:"+jti, "1", sessionTTL); err != nil {
		return err
	}
	return kv.Set(ctx, currentKey, jti, sessionTTL)
}

func storeRefreshToken(ctx context.Context, kv kvstore.KVStore, playerID, refreshToken string) error {
	if err := kv.Set(ctx, fmt.Sprintf("refresh:%s", playerID), refreshToken, refreshTTL); err != nil {
		return err
	}
	return kv.Set(ctx, fmt.Sprintf("refresh_lookup:%s", refreshToken), playerID, refreshTTL)
}

// invalidateRefreshLookup removes the orphaned refresh_lookup entry for an old token
// so a leaked refresh token cannot be replayed once it has been rotated.
func invalidateRefreshLookup(ctx context.Context, kv kvstore.KVStore, refreshToken string) {
	kv.Del(ctx, fmt.Sprintf("refresh_lookup:%s", refreshToken))
}

func lookupRefreshToken(ctx context.Context, kv kvstore.KVStore, refreshToken string) (string, error) {
	return kv.Get(ctx, fmt.Sprintf("refresh_lookup:%s", refreshToken))
}

func getStoredRefreshToken(ctx context.Context, kv kvstore.KVStore, playerID string) (string, error) {
	return kv.Get(ctx, fmt.Sprintf("refresh:%s", playerID))
}

const (
	refreshLockTTL     = 10 * time.Second
	refreshCooldownTTL = 3 * time.Second
)

func acquireRefreshLock(ctx context.Context, kv kvstore.KVStore, playerID string) (bool, error) {
	return kv.SetNX(ctx, fmt.Sprintf("refresh_lock:%s", playerID), "1", refreshLockTTL)
}

func releaseRefreshLock(kv kvstore.KVStore, playerID string) {
	kv.Del(context.Background(), fmt.Sprintf("refresh_lock:%s", playerID))
}

func setRefreshCooldown(ctx context.Context, kv kvstore.KVStore, playerID string) error {
	return kv.Set(ctx, fmt.Sprintf("refresh_cooldown:%s", playerID), "1", refreshCooldownTTL)
}

func isRefreshCoolingDown(ctx context.Context, kv kvstore.KVStore, playerID string) (bool, error) {
	_, err := kv.Get(ctx, fmt.Sprintf("refresh_cooldown:%s", playerID))
	if errors.Is(err, kvstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// logoutSession removes the jti session. If jti matches the current active session,
// the current pointer and refresh token are also removed.
func logoutSession(ctx context.Context, kv kvstore.KVStore, playerID, jti string) error {
	curKey := fmt.Sprintf("session:current:%s", playerID)
	refreshKey := fmt.Sprintf("refresh:%s", playerID)

	storedJTI, _ := kv.Get(ctx, curKey)
	if storedJTI == jti {
		kv.Del(ctx, "session:"+jti, curKey, refreshKey)
	} else {
		kv.Del(ctx, "session:"+jti)
	}
	return nil
}

func claimTicket(ctx context.Context, kv kvstore.KVStore, hash string) (bool, error) {
	return kv.SetNX(ctx, fmt.Sprintf("ticket:used:%s", hash), "1", ticketTTL)
}

func releaseTicket(kv kvstore.KVStore, hash string) {
	kv.Del(context.Background(), fmt.Sprintf("ticket:used:%s", hash))
}
