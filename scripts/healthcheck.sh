#!/bin/bash
# External health check for stone-server.
# Checks GET /health and sends a Slack webhook alert when the service is down
# or has recovered. A state file prevents duplicate alerts within ALERT_COOLDOWN.
#
# Run every minute via cron (see scripts/healthcheck.cron).
#
# Required environment variables:
#   HEALTH_URL          Full URL of the health endpoint
#                       e.g. https://yourdomain.com/api/v1/health
#   SLACK_WEBHOOK_URL   Incoming Webhook URL from Slack App settings
#
# Optional:
#   SERVICE_NAME        Display name in alerts  (default: stone-server)
#   ALERT_COOLDOWN      Seconds before re-alerting on sustained failure (default: 1800)
#   HTTP_TIMEOUT        curl timeout in seconds  (default: 10)
#   STATE_FILE          Path to state file        (default: /tmp/stone-server-hc-state)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 1. Project .env (non-secret defaults — tracked in git, never has real creds)
ENV_FILE="${COMPOSE_DIR:-$(dirname "$SCRIPT_DIR")}/.env"
if [[ -f "$ENV_FILE" ]]; then
    set -a; source "$ENV_FILE"; set +a
fi

# 2. Production secrets file — overrides the project .env.
#    Create this file on the server; do NOT commit it to git.
#    Minimal contents:
#      HEALTH_URL=https://yourdomain.com/api/v1/health
#      SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T.../B.../...
MONITOR_ENV="${MONITOR_ENV:-/etc/stone-server/monitor.env}"
if [[ -f "$MONITOR_ENV" ]]; then
    set -a; source "$MONITOR_ENV"; set +a
fi

HEALTH_URL="${HEALTH_URL:-}"
SLACK_WEBHOOK_URL="${SLACK_WEBHOOK_URL:-}"
SERVICE_NAME="${SERVICE_NAME:-stone-server}"
ALERT_COOLDOWN="${ALERT_COOLDOWN:-1800}"
HTTP_TIMEOUT="${HTTP_TIMEOUT:-10}"
STATE_FILE="${STATE_FILE:-/tmp/stone-server-hc-state}"

if [[ -z "$HEALTH_URL" ]]; then
    echo "ERROR: HEALTH_URL is not set" >&2
    exit 1
fi

# ── Helpers ──────────────────────────────────────────────────────────────────

slack_post() {
    local color="$1" icon="$2" title="$3" message="$4"
    local payload
    payload=$(printf '{"attachments":[{"color":"%s","blocks":[{"type":"section","text":{"type":"mrkdwn","text":"%s *%s*\\n%s\\n*Detected:* %s UTC"}}]}]}' \
        "$color" "$icon" "$title" "$message" "$(date -u '+%Y-%m-%d %H:%M:%S')")
    curl -sf --max-time 10 -X POST -H 'Content-type: application/json' \
        --data "$payload" "$SLACK_WEBHOOK_URL" || true
}

send_down_alert() {
    local status="$1"
    echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] ALERT: $SERVICE_NAME is DOWN (status: $status)"
    [[ -z "$SLACK_WEBHOOK_URL" ]] && return
    slack_post "danger" ":red_circle:" \
        "${SERVICE_NAME} DOWN" \
        "*URL:* ${HEALTH_URL}\\n*HTTP Status:* ${status}"
}

send_recovery_alert() {
    echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] RECOVERED: $SERVICE_NAME is back UP"
    [[ -z "$SLACK_WEBHOOK_URL" ]] && return
    slack_post "good" ":large_green_circle:" \
        "${SERVICE_NAME} RECOVERED" \
        "*URL:* ${HEALTH_URL}"
}

# ── State file format: "<STATUS> <TIMESTAMP>" ─────────────────────────────
# STATUS = "up" | "down"
# TIMESTAMP = epoch seconds of last alert

read_state() {
    if [[ -f "$STATE_FILE" ]]; then
        cat "$STATE_FILE"
    else
        echo "up 0"
    fi
}

write_state() { echo "$1 $2" > "$STATE_FILE"; }

# ── Health check ─────────────────────────────────────────────────────────────

HTTP_STATUS=$(curl -sf -o /dev/null -w "%{http_code}" \
    --max-time "$HTTP_TIMEOUT" "$HEALTH_URL" 2>/dev/null || echo "000")

NOW=$(date +%s)
read -r PREV_STATUS LAST_ALERT_AT < <(read_state)

if [[ "$HTTP_STATUS" == "200" ]]; then
    if [[ "$PREV_STATUS" == "down" ]]; then
        send_recovery_alert
        write_state "up" "$NOW"
    fi
    # No-op when already "up"
else
    SECONDS_SINCE_ALERT=$(( NOW - LAST_ALERT_AT ))
    if [[ "$PREV_STATUS" == "up" || "$SECONDS_SINCE_ALERT" -ge "$ALERT_COOLDOWN" ]]; then
        send_down_alert "$HTTP_STATUS"
        write_state "down" "$NOW"
    fi
fi
