# T5-03 Feedback v3

- Task: `T5-03`
- Reviewed on: `2026-05-06`
- Verdict: `HOLD`

## Findings

### 1. The load-test auth check still fails on realistic reruns because `409 TICKET_USED` is not treated as expected

- Severity: `P1`
- File: [scripts/load-test/scenario.js](/F:/stone_server/scripts/load-test/scenario.js:115)
- Summary: In the latest full `CCU 200` re-run, the `/auth/steam` step produced a mix of `200`, `429`, and `409` responses. The scenario currently checks only for `200 || 429`, so the k6 run still reports failed checks even though `409 TICKET_USED` is a realistic outcome when deterministic mock tickets are reused across repeated validation runs.
- Impact: T5-03 is supposed to provide a repeatable load-validation path. Right now the authored scenario can still fail its own checks during normal re-verification, so this task cannot yet be considered fully complete.

## Verification

- `go test ./...`: passed

### Runtime checks

- Full load run via Docker k6 against a diag-enabled app instance:
  - `VU_COUNT=200`, `RAMP_SECS=30`, `STEADY_SECS=180`
  - results:
    - overall `http_req_duration p(95)=4.37ms`
    - `http_req_failed rate=0.00%`
    - `http_reqs=37510`, about `162.3 req/s`
    - `pool_total_conns` max observed: `4`
- `/auth/steam` status mix observed during that run:
  - `200`: `4`
  - `409`: `6`
  - `429`: `190`
- Memory diagnostics from `diag` logs after the latest fix:
  - first observed `heap_alloc_bytes=1163424`
  - peak observed `heap_alloc_bytes=4410328`
  - later recovered to about `2303736`

## Notes

- The previous memory-recovery finding appears resolved in this revision. Heap usage now falls back close to baseline after load, and DB pool usage stayed well below the task limit.
- The remaining blocker is the scenario's own `/auth/steam` expectation. As written, repeated validation runs can still end with failed checks even when the system behavior is otherwise acceptable.
- [dashboard.html](/F:/stone_server/dashboard.html:232) default status was moved back to `T5-03 = review in progress`.
