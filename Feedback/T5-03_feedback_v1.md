# T5-03 Feedback v1

- Task: `T5-03`
- Reviewed on: `2026-05-06`
- Verdict: `보류`

## Findings

### 1. The default k6 target points at a host port that is not exposed by this Compose stack

- Severity: `P1`
- File: [scripts/load-test/run.sh](/F:/stone_server/scripts/load-test/run.sh:20)
- Summary: The runner defaults `TARGET_URL` to `http://localhost:8080`, and the scenario repeats that same default in [scenario.js](/F:/stone_server/scripts/load-test/scenario.js:26). But this repo's Compose stack does not publish the app container on host port `8080`; only nginx ports `80/443` are exposed in [docker-compose.yml](/F:/stone_server/docker-compose.yml:48).
- Impact: In the actual deployed/local Compose environment, the load test fails out of the box before it can validate T5-03. I confirmed `curl http://127.0.0.1:8080/api/v1/health` fails while `https://127.0.0.1/api/v1/health` succeeds.

### 2. The load scenario never exercises `/auth/steam`, one of the required review APIs

- Severity: `P1`
- File: [scripts/load-test/scenario.js](/F:/stone_server/scripts/load-test/scenario.js:65)
- Summary: Instead of loading `POST /api/v1/auth/steam`, setup mints JWTs through the dev-only shortcut `GET /api/v1/internal/dev-token`, and the default VU loop only hits `/player/state`, `/player/clicks`, and `/gacha/pull`.
- Impact: One of the task's explicitly required major API scenarios is not covered by the performance test, so the reported CCU/RPS/latency numbers would not represent the full target surface.

### 3. The task's DB-connection and memory-leak acceptance criteria are not actually measured

- Severity: `P1`
- File: [scripts/load-test/scenario.js](/F:/stone_server/scripts/load-test/scenario.js:14)
- Summary: The script declares latency/error targets, but there is no instrumentation or collection path for DB pool usage (`<= 50`) or post-test memory recovery. I also found no server-side pool-stat logging or memory sampling code in the repo.
- Impact: Even if k6 passes the latency thresholds, the task's required checks for connection-pool ceiling and memory leak cannot be concluded from the current implementation.

## Verification

- Code review:
  - [run.sh](/F:/stone_server/scripts/load-test/run.sh:20) default target is `http://localhost:8080`
  - [scenario.js](/F:/stone_server/scripts/load-test/scenario.js:71) uses `/api/v1/internal/dev-token` in setup instead of `/auth/steam`
  - [scenario.js](/F:/stone_server/scripts/load-test/scenario.js:112) VU traffic covers `state`, `clicks`, `gacha` only
  - Repository search found no DB pool stat logging or runtime memory sampling for T5-03 verification

### Runtime checks

- `docker compose ps`: app/nginx/postgres/redis all running
- `curl http://127.0.0.1:8080/api/v1/health`:
  - failed to connect
- `curl -k https://127.0.0.1/api/v1/health`:
  - `200 OK`

## Notes

- `k6` itself is not installed in this environment, so I could not execute the authored k6 scenario directly.
- That did not block the core findings above, because the default target mismatch and acceptance-coverage gaps are evident from the checked-in scripts and Compose exposure.
- [dashboard.html](/F:/stone_server/dashboard.html:232) default status was moved back to `T5-03 = review in progress`.
