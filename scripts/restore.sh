#!/bin/bash
# stone-server DB restore script.
#
# Restores a pg_dump backup file created by scripts/backup.sh into the
# running Compose postgres container, then verifies data consistency and
# measures RTO (time from script start to GET /health 200).
#
# Usage:
#   ./scripts/restore.sh <backup-file.pgdump>
#
# Environment variables:
#   COMPOSE_DIR    Directory containing docker-compose.yml  (default: script parent)
#   DB_NAME        PostgreSQL database name                 (default: stone_server)
#   DB_USER        PostgreSQL user                          (default: stone)
#   DB_PASSWORD    PostgreSQL password                      (default: from .env)
#   HEALTH_URL     URL polled for health check              (default: https://localhost/api/v1/health)
#   HEALTH_TIMEOUT Seconds to wait for /health 200          (default: 120)
#
# Exit codes:
#   0  Restore succeeded, /health 200, consistency check passed
#   1  Missing argument / file not found
#   2  pg_restore failed
#   3  /health did not return 200 within HEALTH_TIMEOUT seconds
#   4  Referential integrity violations detected (orphaned rows)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_DIR="${COMPOSE_DIR:-$(dirname "$SCRIPT_DIR")}"

# ── Source project .env for DB credentials ───────────────────────────────────
ENV_FILE="${COMPOSE_DIR}/.env"
if [[ -f "$ENV_FILE" ]]; then
    set -a; source "$ENV_FILE"; set +a
fi

DB_NAME="${DB_NAME:-stone_server}"
DB_USER="${DB_USER:-stone}"
DB_PASSWORD="${DB_PASSWORD:-}"
HEALTH_URL="${HEALTH_URL:-https://localhost/api/v1/health}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-120}"

# ── Validate arguments ────────────────────────────────────────────────────────
if [[ $# -lt 1 ]]; then
    echo "Usage: $0 <backup-file.pgdump>" >&2
    exit 1
fi

BACKUP_FILE="$1"
if [[ ! -f "$BACKUP_FILE" ]]; then
    echo "ERROR: backup file not found: ${BACKUP_FILE}" >&2
    exit 1
fi

BACKUP_SIZE=$(du -sh "$BACKUP_FILE" | cut -f1)

ts() { date -u +%Y-%m-%dT%H:%M:%SZ; }
log() { echo "[$(ts)] $*"; }

RTO_START=$(date +%s)
log "=== stone-server DB restore ==="
log "Backup file : ${BACKUP_FILE} (${BACKUP_SIZE})"
log "Database    : ${DB_NAME}"
log "Health URL  : ${HEALTH_URL}"

# ── Step 1: Stop app container ────────────────────────────────────────────────
log "--- Step 1: stopping app container"
docker compose --project-directory "$COMPOSE_DIR" stop app
log "    app stopped"

# ── Step 2: pg_restore ────────────────────────────────────────────────────────
# --clean --if-exists: drops existing objects before recreating them so the
# restore is idempotent (safe to re-run on an already-populated database).
# --no-owner / --no-privileges: skip ownership statements that can fail if the
# backup was taken with a different superuser.
log "--- Step 2: running pg_restore"
RESTORE_START=$(date +%s)

if PGPASSWORD="$DB_PASSWORD" docker compose --project-directory "$COMPOSE_DIR" \
    exec -T postgres \
    pg_restore \
        -U "$DB_USER" \
        -d "$DB_NAME" \
        --clean --if-exists \
        --no-owner --no-privileges \
        --exit-on-error \
    < "$BACKUP_FILE"; then
    RESTORE_END=$(date +%s)
    log "    pg_restore complete ($(( RESTORE_END - RESTORE_START ))s)"
else
    log "ERROR: pg_restore failed" >&2
    log "Restarting app to restore service..." >&2
    docker compose --project-directory "$COMPOSE_DIR" start app
    exit 2
fi

# ── Step 3: Data consistency check ───────────────────────────────────────────
log "--- Step 3: consistency check"

PSQL() {
    PGPASSWORD="$DB_PASSWORD" docker compose --project-directory "$COMPOSE_DIR" \
        exec -T postgres psql -U "$DB_USER" -d "$DB_NAME" -tAc "$1"
}

PLAYERS=$(PSQL "SELECT COUNT(*) FROM players")
STATES=$(PSQL "SELECT COUNT(*) FROM player_states")
INVENTORIES=$(PSQL "SELECT COUNT(*) FROM inventories")
GACHA_LOGS=$(PSQL "SELECT COUNT(*) FROM gacha_logs")

# Orphaned player_states (no matching players row) — must be 0.
ORPHAN_STATES=$(PSQL "
    SELECT COUNT(*) FROM player_states ps
    LEFT JOIN players p ON p.id = ps.player_id
    WHERE p.id IS NULL
")

# Orphaned inventories — must be 0.
ORPHAN_INV=$(PSQL "
    SELECT COUNT(*) FROM inventories i
    LEFT JOIN players p ON p.id = i.player_id
    WHERE p.id IS NULL
")

log "    players        : ${PLAYERS}"
log "    player_states  : ${STATES}"
log "    inventories    : ${INVENTORIES}"
log "    gacha_logs     : ${GACHA_LOGS}"
log "    orphan states  : ${ORPHAN_STATES}  (expected 0)"
log "    orphan inv.    : ${ORPHAN_INV}  (expected 0)"

CONSISTENCY_OK=true
if [[ "$ORPHAN_STATES" -ne 0 || "$ORPHAN_INV" -ne 0 ]]; then
    log "WARNING: referential integrity violations detected" >&2
    CONSISTENCY_OK=false
fi

# ── Step 4: Restart app ───────────────────────────────────────────────────────
log "--- Step 4: starting app container"
docker compose --project-directory "$COMPOSE_DIR" start app
log "    app started"

# ── Step 5: Wait for /health 200 ─────────────────────────────────────────────
log "--- Step 5: waiting for /health to return 200 (timeout: ${HEALTH_TIMEOUT}s)"

DEADLINE=$(( $(date +%s) + HEALTH_TIMEOUT ))
HEALTH_OK=false
while [[ $(date +%s) -lt $DEADLINE ]]; do
    STATUS=$(curl -sk -o /dev/null -w "%{http_code}" \
        --max-time 5 "$HEALTH_URL" 2>/dev/null || echo "000")
    if [[ "$STATUS" == "200" ]]; then
        HEALTH_OK=true
        break
    fi
    sleep 2
done

RTO_END=$(date +%s)
RTO=$(( RTO_END - RTO_START ))

log "--- Results"
log "    /health status    : $( [[ $HEALTH_OK == true ]] && echo '200 OK' || echo 'TIMEOUT' )"
log "    consistency       : $( [[ $CONSISTENCY_OK == true ]] && echo 'PASS' || echo 'FAIL (see warnings above)' )"
log "    RTO               : ${RTO}s (target: <3600s)"
log "=== restore complete ==="

if [[ "$HEALTH_OK" != true ]]; then
    log "ERROR: /health did not return 200 within ${HEALTH_TIMEOUT}s" >&2
    exit 3
fi

if [[ "$CONSISTENCY_OK" != true ]]; then
    log "ERROR: referential integrity violations detected — restore is not clean" >&2
    exit 4
fi
