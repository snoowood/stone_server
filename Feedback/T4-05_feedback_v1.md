# T4-05 Feedback v1

- Task: `T4-05`
- Reviewed on: `2026-05-05`
- Verdict: `보류`

## Findings

### 1. Failed retry items are retried again immediately instead of waiting 1 minute

- Severity: `P1`
- File: [internal/achievement/worker.go](/F:/stone_server/internal/achievement/worker.go:74)
- Summary: `processBatch()` drains the Redis list until it is empty, but `processOne()` pushes a failed item straight back onto the same `ach:retry` list before returning. That lets the same batch loop pop the same item again right away in the same tick.
- Impact: The worker does not honor the task's required "retry within 1 minute" behavior. During a Steam outage it can spin on the same item continuously, ignore `next_retry_at`, and hammer the Steam API instead of backing off for one minute.

## Verification

- `go test ./internal/achievement -count=1 -v`: passed
- `go test ./...`: passed

### Code review checks

- Worker is started from [main.go](/F:/stone_server/cmd/server/main.go:76) with a shared shutdown context and wait group.
- Panic recovery exists in [worker.go](/F:/stone_server/internal/achievement/worker.go:51) and prevents a worker panic from crashing the whole server.
- Graceful shutdown wiring exists in [main.go](/F:/stone_server/cmd/server/main.go:130) and the unit test for `wg.Wait()` shutdown passes.
- Retry metadata is written to `achievement_retry_queue.next_retry_at`, but the worker never reads that value when deciding whether an item is due.

### Why this blocks the task

- The review goal says Steam failure should retry "within 1 minute".
- The current loop retries failed items immediately in the same batch, so the one-minute retry contract is not met even though the ticker interval is set to one minute.

## Notes

- The existing unit tests cover panic recovery, queue draining, and graceful shutdown, but they do not currently cover the delayed retry requirement after a Steam failure.
- [dashboard.html](/F:/stone_server/dashboard.html:232) default status was moved back to `T4-05 = review in progress`.
