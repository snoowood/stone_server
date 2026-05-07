#!/bin/bash
# Daily PostgreSQL backup for stone-server.
#
# Usage:
#   ./scripts/backup.sh
#
# Environment variables (can be set in the shell or via .env):
#   BACKUP_DIR       Local directory to store backups  (default: /var/backups/stone-server)
#   COMPOSE_DIR      Directory containing docker-compose.yml  (default: script's parent dir)
#   DB_NAME          PostgreSQL database name  (default: stone_server)
#   DB_USER          PostgreSQL user           (default: stone)
#   DB_PASSWORD      PostgreSQL password       (default: empty)
#   RETAIN_DAYS      Number of daily backups to keep  (default: 7)
#   S3_BUCKET        If set, upload to s3://${S3_BUCKET}/stone-server/ after local backup
#                    Requires AWS CLI configured with appropriate credentials.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_DIR="${COMPOSE_DIR:-$(dirname "$SCRIPT_DIR")}"

# Source .env from the project root if present and not already exported.
ENV_FILE="${COMPOSE_DIR}/.env"
if [[ -f "$ENV_FILE" ]]; then
    set -a
    # shellcheck source=/dev/null
    source "$ENV_FILE"
    set +a
fi

BACKUP_DIR="${BACKUP_DIR:-/var/backups/stone-server}"
DB_NAME="${DB_NAME:-stone_server}"
DB_USER="${DB_USER:-stone}"
DB_PASSWORD="${DB_PASSWORD:-}"
RETAIN_DAYS="${RETAIN_DAYS:-7}"

mkdir -p "$BACKUP_DIR"

TIMESTAMP=$(date -u +%Y%m%d_%H%M%S)
FILENAME="${DB_NAME}_${TIMESTAMP}.pgdump"
FILEPATH="${BACKUP_DIR}/${FILENAME}"

echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] Starting backup → ${FILEPATH}"

# Run pg_dump inside the postgres container.
# -Fc: custom format (compressed, supports parallel pg_restore).
PGPASSWORD="$DB_PASSWORD" docker compose \
    --project-directory "$COMPOSE_DIR" \
    exec -T postgres \
    pg_dump -U "$DB_USER" -Fc "$DB_NAME" \
    > "$FILEPATH"

FILESIZE=$(du -sh "$FILEPATH" | cut -f1)
echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] Backup complete: ${FILEPATH} (${FILESIZE})"

# Delete backups older than RETAIN_DAYS (keeps exactly 7 rolling days).
# -mtime +7 matches files modified more than 7×24 h ago (i.e. day 8 and older).
DELETED=$(find "$BACKUP_DIR" -name "${DB_NAME}_*.pgdump" -mtime "+${RETAIN_DAYS}" -print -delete | wc -l)
if [[ "$DELETED" -gt 0 ]]; then
    echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] Pruned ${DELETED} backup(s) older than ${RETAIN_DAYS} days"
fi

# Optional: upload to S3 for off-host durability.
if [[ -n "${S3_BUCKET:-}" ]]; then
    S3_KEY="stone-server/${FILENAME}"
    echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] Uploading to s3://${S3_BUCKET}/${S3_KEY}"
    aws s3 cp "$FILEPATH" "s3://${S3_BUCKET}/${S3_KEY}" --storage-class STANDARD_IA
    echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] S3 upload complete"

    # Remove S3 objects older than RETAIN_DAYS as well.
    CUTOFF=$(date -u -d "${RETAIN_DAYS} days ago" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
             || date -u -v-"${RETAIN_DAYS}"d +%Y-%m-%dT%H:%M:%SZ)
    aws s3api list-objects-v2 \
        --bucket "$S3_BUCKET" \
        --prefix "stone-server/${DB_NAME}_" \
        --query "Contents[?LastModified<='${CUTOFF}'].Key" \
        --output text \
    | tr '\t' '\n' \
    | grep -v '^$' \
    | while read -r key; do
        aws s3 rm "s3://${S3_BUCKET}/${key}"
        echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] Deleted old S3 object: ${key}"
    done
fi
