package achievement

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/store"
)

// panicSteamClient panics on SetAchievement to test panic recovery.
type panicSteamClient struct{}

func (p *panicSteamClient) SetAchievement(_ context.Context, _, _ string) error {
	panic("simulated steam panic")
}

// mockWorkerDB implements workerDB for worker unit tests.
type mockWorkerDB struct {
	queryRowFns []func() store.Row
	execFns     []func() (store.Result, error)
	execCalls   int
}

func (m *mockWorkerDB) QueryRow(_ context.Context, _ string, _ ...any) store.Row {
	fn := m.queryRowFns[0]
	m.queryRowFns = m.queryRowFns[1:]
	return fn()
}

func (m *mockWorkerDB) Exec(_ context.Context, _ string, _ ...any) (store.Result, error) {
	m.execCalls++
	fn := m.execFns[0]
	m.execFns = m.execFns[1:]
	return fn()
}

func steamIDRow(id string) func() store.Row {
	return func() store.Row {
		return rowScan(func(dest ...any) error {
			*dest[0].(*string) = id
			return nil
		})
	}
}

// queueLen pops all items from key and returns the count. Destructive.
func queueLen(kv kvstore.KVStore, key string) int {
	count := 0
	for {
		_, err := kv.RPop(context.Background(), key)
		if err != nil {
			break
		}
		count++
	}
	return count
}

// ---- processOne tests ----

func TestProcessOne_SteamSuccess(t *testing.T) {
	kv := kvstore.NewMemStore()
	db := &mockWorkerDB{
		queryRowFns: []func() store.Row{steamIDRow("steam123")},
		execFns: []func() (store.Result, error){
			execOK(), // UPDATE player_achievements SET steam_synced = true
			execOK(), // UPDATE achievement_retry_queue SET resolved_at
		},
	}
	w := &Worker{db: db, kv: kv, steam: &mockSteam{}}
	w.processOne(context.Background(), "player-1:ACH_FIRST_PULL")

	if db.execCalls != 2 {
		t.Fatalf("expected 2 exec calls (steam_synced + resolved_at), got %d", db.execCalls)
	}
}

func TestProcessOne_SteamFailure_ReturnsTrueAndUpdatesRetryCount(t *testing.T) {
	kv := kvstore.NewMemStore()
	db := &mockWorkerDB{
		queryRowFns: []func() store.Row{steamIDRow("steam123")},
		execFns: []func() (store.Result, error){
			execOK(), // UPDATE retry_count + 1
		},
	}
	w := &Worker{db: db, kv: kv, steam: &mockSteam{err: errors.New("steam down")}, tickInterval: time.Minute}
	requeue := w.processOne(context.Background(), "player-1:ACH_FIRST_PULL")

	if !requeue {
		t.Fatal("expected processOne to return true (requeue) on Steam failure")
	}
	if db.execCalls != 1 {
		t.Fatalf("expected 1 exec call (retry_count update), got %d", db.execCalls)
	}
	// processOne must NOT push to KV queue — that is processBatch's responsibility.
	if queueLen(kv, retryQueue) != 0 {
		t.Fatal("processOne must not push to KV directly")
	}
}

func TestProcessOne_MalformedItem_Dropped(t *testing.T) {
	kv := kvstore.NewMemStore()
	db := &mockWorkerDB{}
	w := &Worker{db: db, kv: kv, steam: &mockSteam{}}
	w.processOne(context.Background(), "no-colon-here") // should not panic

	if db.execCalls != 0 {
		t.Fatal("malformed item should not trigger any DB calls")
	}
}

func TestProcessOne_PlayerNotFound_Dropped(t *testing.T) {
	kv := kvstore.NewMemStore()
	db := &mockWorkerDB{
		queryRowFns: []func() store.Row{
			func() store.Row { return rowErrH(sql.ErrNoRows) },
		},
	}
	w := &Worker{db: db, kv: kv, steam: &mockSteam{}}
	w.processOne(context.Background(), "player-ghost:ACH_FIRST_PULL")

	if queueLen(kv, retryQueue) != 0 {
		t.Fatal("item for non-existent player should be dropped, not re-queued")
	}
}

func TestProcessOne_DBLookupError_ReturnsTrue(t *testing.T) {
	kv := kvstore.NewMemStore()
	db := &mockWorkerDB{
		queryRowFns: []func() store.Row{
			func() store.Row { return rowErrH(errors.New("db error")) },
		},
	}
	w := &Worker{db: db, kv: kv, steam: &mockSteam{}}
	requeue := w.processOne(context.Background(), "player-1:ACH_FIRST_PULL")

	if !requeue {
		t.Fatal("expected processOne to return true (requeue) on DB lookup error")
	}
	// processOne must NOT push to KV — processBatch owns that.
	if queueLen(kv, retryQueue) != 0 {
		t.Fatal("processOne must not push to KV directly")
	}
}

// ---- processBatch tests ----

func TestProcessBatch_EmptyQueue(t *testing.T) {
	kv := kvstore.NewMemStore()
	w := &Worker{db: &mockWorkerDB{}, kv: kv, steam: &mockSteam{}}
	w.processBatch(context.Background()) // must not panic
}

func TestProcessBatch_DrainsAllItems(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	kv.LPush(ctx, retryQueue, "p1:ACH_FIRST_PULL")
	kv.LPush(ctx, retryQueue, "p2:ACH_COLLECTOR")

	db := &mockWorkerDB{
		queryRowFns: []func() store.Row{steamIDRow("s1"), steamIDRow("s2")},
		execFns:     []func() (store.Result, error){execOK(), execOK(), execOK(), execOK()},
	}
	w := &Worker{db: db, kv: kv, steam: &mockSteam{}}
	w.processBatch(ctx)

	if queueLen(kv, retryQueue) != 0 {
		t.Fatal("expected empty queue after batch")
	}
}

// TestProcessBatch_FailedItemNotRetriedInSameTick is the regression test for the
// P1 finding: a failed item must not be popped and processed again in the same
// batch. It verifies that processOne is called exactly once per item per tick,
// even when Steam fails, by supplying only one queryRowFn and one execFn —
// a second call would panic on the empty slice.
func TestProcessBatch_FailedItemNotRetriedInSameTick(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()
	kv.LPush(ctx, retryQueue, "p1:ACH_FIRST_PULL")

	db := &mockWorkerDB{
		queryRowFns: []func() store.Row{steamIDRow("steam123")},
		execFns:     []func() (store.Result, error){execOK()},
	}
	w := &Worker{db: db, kv: kv, steam: &mockSteam{err: errors.New("steam down")}, tickInterval: time.Minute}
	w.processBatch(ctx) // must not panic (would panic if processOne called twice)

	// Item re-queued for the next tick, not retried in this one.
	if queueLen(kv, retryQueue) != 1 {
		t.Fatal("expected 1 item in queue after batch (deferred re-queue)")
	}
	if db.execCalls != 1 {
		t.Fatalf("processOne should have been called exactly once, execCalls=%d", db.execCalls)
	}
}

// TestProcessBatch_FailedItemsDoNotLeapfrogNewProducer verifies that when items
// fail and a NEW item is LPush'd by the producer during the batch, the failed
// items are re-queued at the tail (oldest position) so the new item is processed
// AFTER them on the next tick — not before. Regression test for the ordering bug.
func TestProcessBatch_FailedItemsDoNotLeapfrogNewProducer(t *testing.T) {
	kv := kvstore.NewMemStore()
	ctx := context.Background()

	// Initial queue: two items that will fail.
	kv.LPush(ctx, retryQueue, "p1:ACH_FIRST_PULL")
	kv.LPush(ctx, retryQueue, "p2:ACH_RARE_UNLOCK")

	db := &mockWorkerDB{
		queryRowFns: []func() store.Row{steamIDRow("s1"), steamIDRow("s2")},
		execFns:     []func() (store.Result, error){execOK(), execOK()}, // retry_count updates
	}
	w := &Worker{db: db, kv: kv, steam: &mockSteam{err: errors.New("steam down")}, tickInterval: time.Minute}

	w.processBatch(ctx)

	// Producer pushes a brand-new item AFTER the batch completes.
	kv.LPush(ctx, retryQueue, "p3:ACH_LEGENDARY")

	// Next tick should pop in original FIFO order: p1, p2 (the failures), then p3 (newest).
	want := []string{"p1:ACH_FIRST_PULL", "p2:ACH_RARE_UNLOCK", "p3:ACH_LEGENDARY"}
	for i, expect := range want {
		got, err := kv.RPop(ctx, retryQueue)
		if err != nil {
			t.Fatalf("position %d: RPop err: %v", i, err)
		}
		if got != expect {
			t.Errorf("position %d: got %q, want %q", i, got, expect)
		}
	}
}

// ---- run / panic recovery tests ----

func TestRun_PanicRecovery(t *testing.T) {
	kv := kvstore.NewMemStore()
	kv.LPush(context.Background(), retryQueue, "p1:ACH_FIRST_PULL")

	db := &mockWorkerDB{
		queryRowFns: []func() store.Row{steamIDRow("steam123")},
	}
	w := &Worker{db: db, kv: kv, steam: &panicSteamClient{}, tickInterval: 5 * time.Millisecond}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	// run() must recover the panic internally and return after ctx expires.
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic leaked out of run(): %v", r)
			}
		}()
		w.run(ctx)
	}()

	select {
	case <-done:
		// good
	case <-time.After(time.Second):
		t.Fatal("run() did not return within 1 second after ctx cancelled")
	}
}

func TestStart_GracefulShutdown(t *testing.T) {
	kv := kvstore.NewMemStore()
	w := &Worker{db: &mockWorkerDB{}, kv: kv, steam: &mockSteam{}, tickInterval: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	w.Start(ctx, &wg)

	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		// good
	case <-time.After(time.Second):
		t.Fatal("worker did not shut down within 1 second after ctx cancel")
	}
}
