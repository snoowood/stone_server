# T5-04 Feedback v2

- Task: `T5-04`
- Reviewed on: `2026-05-06`
- Verdict: `PASS`

## Findings

- No new findings.

## Verification

- `go test ./...`: passed

### Restore rehearsal

- Baseline counts before the rehearsal:
  - `players=202`
  - `player_states=202`
  - `inventories=202`
  - `gacha_logs=229`
- Inserted one temporary player set after the backup:
  - counts became `203 / 203 / 203 / 230`
- Ran [restore.sh](/F:/stone_server/scripts/restore.sh) against:
  - `/mnt/f/stone_server/tmp_backups/stone_server_20260506_143600.pgdump`
- Restore result:
  - `pg_restore` completed successfully
  - `/health` recovered to `200 OK`
  - reported `RTO = 5s`
  - reported `consistency = PASS`
- Post-restore verification:
  - counts returned to `202 / 202 / 202 / 229`
  - temporary test player `44444444-4444-4444-4444-444444444444` was removed
  - `/api/v1/health` still returned `{"status":"ok","db":"ok","cache":"ok","version":"1.0.0"}`

## Notes

- The previous blocker is fixed. [restore.sh](/F:/stone_server/scripts/restore.sh:170) now exits non-zero on a failed consistency check, so operators and automation will not treat an integrity-failed restore as success.
- The end-to-end rehearsal still proves the intended recovery path: backup snapshot restoration, record-count rollback to the snapshot point, and service recovery within the target window.
- [dashboard.html](/F:/stone_server/dashboard.html:232) already shows the default status for `T5-04` as `완료`.
