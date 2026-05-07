# T5-04 Feedback v1

- Task: `T5-04`
- Reviewed on: `2026-05-06`
- Verdict: `HOLD`

## Findings

### 1. `restore.sh` still exits successfully even when its own consistency check fails

- Severity: `P1`
- File: [scripts/restore.sh](/F:/stone_server/scripts/restore.sh:129)
- Summary: The script sets `CONSISTENCY_OK=false` when orphaned rows are detected, but that flag is only used for logging. The only non-zero exit path at the end is the `/health` timeout, so a restore can print `consistency: FAIL` and still exit `0`.
- Impact: T5-04 is supposed to verify that restored data is valid. In its current form, automation or an operator can treat a restore as successful even though the script itself detected integrity problems, which makes the recovery validation path unreliable.

## Verification

- `go test ./...`: passed

### Restore rehearsal

- Snapshot counts before backup:
  - `players=202`
  - `player_states=202`
  - `inventories=202`
  - `gacha_logs=229`
- Created a fresh backup with [backup.sh](/F:/stone_server/scripts/backup.sh) to:
  - `/mnt/f/stone_server/tmp_backups/stone_server_20260506_143600.pgdump`
- Inserted one temporary test player set after the backup:
  - counts became `203 / 203 / 203 / 230`
- Ran [restore.sh](/F:/stone_server/scripts/restore.sh) against that backup:
  - `pg_restore` completed
  - `/health` recovered to `200 OK`
  - reported `RTO = 4s`
- Post-restore verification:
  - counts returned to `202 / 202 / 202 / 229`
  - temporary test player `11111111-1111-1111-1111-111111111111` was removed
  - orphan counts were `0`

## Notes

- The main restore flow and runbook are close. The end-to-end rehearsal did prove that the backup can restore the database and bring the service back within the target window.
- The remaining blocker is the script's final success condition. A detected consistency failure must not still return exit code `0`.
- [dashboard.html](/F:/stone_server/dashboard.html:232) default status was moved back to `T5-04 = review in progress`.
