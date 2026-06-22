package achievement

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/gensdeis/stone-server/internal/cairn"
	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/sqlitedb"
	"github.com/gensdeis/stone-server/pkg/store"
)

// LOG-3: ReloadPendingRetries rebuilds the queue to exactly the DB pending set
// (resolved_at IS NULL AND retry_count < maxRetries): pending rows are re-enqueued,
// resolved/dead-lettered rows are excluded, and any stale item left in the queue is
// drained (so a prod queue surviving a restart is reconciled, not duplicated).
func TestReloadPendingRetries(t *testing.T) {
	ctx := context.Background()
	raw, err := sqlitedb.New(filepath.Join(t.TempDir(), "ach.db"),
		cairn.Default.SlotCount, int(cairn.Default.PhaseOffset().Seconds()))
	if err != nil {
		t.Fatalf("sqlitedb.New: %v", err)
	}
	defer raw.Close()
	db := store.NewSQLAdapter(raw)
	kv := kvstore.NewMemStore()

	if _, err := db.Exec(ctx, `INSERT INTO players (id, steam_id) VALUES ('p1','s1')`); err != nil {
		t.Fatalf("seed player: %v", err)
	}
	seed := func(id, ach string, retry int, resolved any) {
		if _, err := db.Exec(ctx,
			`INSERT INTO achievement_retry_queue (id, player_id, achievement_id, retry_count, resolved_at)
			 VALUES (?, 'p1', ?, ?, ?)`, id, ach, retry, resolved); err != nil {
			t.Fatalf("seed %s: %v", ach, err)
		}
	}
	const maxRetries = 5
	seed("r1", "ACH_PENDING", 0, nil)                     // pending → reload
	seed("r2", "ACH_RESOLVED", 0, "2026-01-01T00:00:00Z") // already synced → exclude
	seed("r3", "ACH_DEADLETTER", maxRetries, nil)         // retry_count >= cap → exclude

	// A stale item that no longer corresponds to a pending DB row must be drained.
	if err := kv.LPush(ctx, retryQueue, "p1:ACH_STALE"); err != nil {
		t.Fatalf("seed stale queue item: %v", err)
	}

	got, err := ReloadPendingRetries(ctx, db, kv, maxRetries)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got != 1 {
		t.Fatalf("reloaded = %d, want 1 (only the pending row)", got)
	}

	// Queue must now be exactly the DB pending set: p1:ACH_PENDING, nothing else
	// (stale drained, resolved/dead-lettered excluded).
	item, err := kv.RPop(ctx, retryQueue)
	if err != nil || item != "p1:ACH_PENDING" {
		t.Fatalf("queue item = %q (err %v), want p1:ACH_PENDING", item, err)
	}
	if _, err := kv.RPop(ctx, retryQueue); !errors.Is(err, kvstore.ErrNotFound) {
		t.Fatal("queue must hold exactly one reloaded item (stale item should be drained)")
	}
}
