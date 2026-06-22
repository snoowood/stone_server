package kvstore

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type kvEntry struct {
	val       string
	expiresAt time.Time // zero = no expiry
}

func (e *kvEntry) expired() bool {
	return !e.expiresAt.IsZero() && time.Now().After(e.expiresAt)
}

// MemStore is a thread-safe in-memory KVStore with TTL support.
// It replaces Redis for lightweight / development use.
type MemStore struct {
	mu    sync.Mutex
	kv    map[string]*kvEntry
	lists map[string][]string
}

func NewMemStore() *MemStore {
	return &MemStore{
		kv:    make(map[string]*kvEntry),
		lists: make(map[string][]string),
	}
}

func (m *MemStore) getEntry(key string) (*kvEntry, bool) {
	e, ok := m.kv[key]
	if !ok {
		return nil, false
	}
	if e.expired() {
		delete(m.kv, key)
		return nil, false
	}
	return e, true
}

func (m *MemStore) Get(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.getEntry(key)
	if !ok {
		return "", ErrNotFound
	}
	return e.val, nil
}

func (m *MemStore) Set(_ context.Context, key, val string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := &kvEntry{val: val}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	}
	m.kv[key] = e
	return nil
}

func (m *MemStore) SetNX(_ context.Context, key, val string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.getEntry(key); ok {
		return false, nil
	}
	e := &kvEntry{val: val}
	if ttl > 0 {
		e.expiresAt = time.Now().Add(ttl)
	}
	m.kv[key] = e
	return true, nil
}

func (m *MemStore) Del(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.kv, k)
	}
	return nil
}

func (m *MemStore) Exists(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.getEntry(key)
	return ok, nil
}

func (m *MemStore) IncrBy(_ context.Context, key string, n int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var cur int64
	if e, ok := m.getEntry(key); ok {
		v, err := strconv.ParseInt(e.val, 10, 64)
		if err != nil {
			return 0, err
		}
		cur = v
		newVal := cur + n
		e.val = strconv.FormatInt(newVal, 10)
		return newVal, nil
	}
	m.kv[key] = &kvEntry{val: strconv.FormatInt(n, 10)}
	return n, nil
}

func (m *MemStore) Expire(_ context.Context, key string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.kv[key]
	if !ok || e.expired() {
		return nil
	}
	e.expiresAt = time.Now().Add(ttl)
	return nil
}

func (m *MemStore) LPush(_ context.Context, key, val string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lists[key] = append([]string{val}, m.lists[key]...)
	return nil
}

func (m *MemStore) RPush(_ context.Context, key, val string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lists[key] = append(m.lists[key], val)
	return nil
}

func (m *MemStore) RPop(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.lists[key]
	if len(items) == 0 {
		return "", ErrNotFound
	}
	n := len(items)
	val := items[n-1]
	m.lists[key] = items[:n-1]
	return val, nil
}

func (m *MemStore) Ping(_ context.Context) error { return nil }

// StartSweeper launches a goroutine that periodically evicts expired keys until ctx is
// cancelled. MemStore otherwise expires keys only lazily on access (getEntry), so keys
// that are written once and never read again — e.g. refresh_lookup:<token>, a fresh key
// per login that is never re-fetched — accumulate forever, a slow memory leak. Prod uses
// Redis (self-expiring), so this is an opt-in dev-mode safeguard; NewMemStore's signature
// is unchanged. Each pass logs swept/remaining counts at Debug so the leak is observable.
func (m *MemStore) StartSweeper(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				swept, remaining := m.sweep()
				log.Debug().Int("swept", swept).Int("remaining_keys", remaining).Msg("memstore sweep")
			}
		}
	}()
}

// sweep deletes all expired kv entries and returns (swept, remaining) counts.
func (m *MemStore) sweep() (swept, remaining int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, e := range m.kv {
		if e.expired() {
			delete(m.kv, k)
			swept++
		}
	}
	return swept, len(m.kv)
}
