# T5-03 Feedback v4

- Task: `T5-03`
- Reviewed on: `2026-05-06`
- Verdict: `PASS`

## Findings

- No new findings.

## Verification

- `go test ./...`: passed

### Runtime checks

- Full load run via Docker k6 against a diag-enabled app instance:
  - `VU_COUNT=200`, `RAMP_SECS=30`, `STEADY_SECS=180`
  - results:
    - overall `http_req_duration p(95)=4.51ms`
    - `http_req_failed rate=0.00%`
    - `checks_failed=0`
    - `http_reqs=37663`, about `162.4 req/s`
- Scenario checks:
  - `auth/steam: 200|409|429` passed
  - `state: 200` passed
  - `clicks: 200|409|429` passed
  - `gacha: 200|409|429` passed
- Diagnostic logs from the same run:
  - first observed `heap_alloc_bytes=1165248`
  - peak observed `heap_alloc_bytes=4294552`
  - later recovered to about `2201152`
  - `pool_total_conns` max observed: `3`

## Notes

- The previous rerun-check issue is resolved. The load test now cleanly passes even when `/auth/steam` returns `409 TICKET_USED` during repeated validation runs.
- The previous memory-recovery concern also remains resolved in this run. Heap usage settled back close to baseline after ramp-down, and DB pool usage stayed far below the task limit.
- [dashboard.html](/F:/stone_server/dashboard.html:232) already shows the default status for `T5-03` as `완료`.
