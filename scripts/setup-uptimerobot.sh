#!/bin/bash
# Register a 5-minute HTTP(S) monitor on UptimeRobot for the /health endpoint.
#
# UptimeRobot free tier: 50 monitors, 5-minute check interval.
# This script uses the UptimeRobot API v2 to create the monitor and attach
# an alert contact so failures trigger notifications within 5 minutes.
#
# Usage:
#   UPTIMEROBOT_API_KEY=ur123456-... \
#   HEALTH_URL=https://yourdomain.com/api/v1/health \
#   ALERT_CONTACT_ID=0123456 \
#   ./scripts/setup-uptimerobot.sh
#
# To find your ALERT_CONTACT_ID:
#   curl -s -X POST https://api.uptimerobot.com/v2/getAlertContacts \
#       -d "api_key=${UPTIMEROBOT_API_KEY}&format=json" | jq .
set -euo pipefail

API_KEY="${UPTIMEROBOT_API_KEY:-}"
HEALTH_URL="${HEALTH_URL:-}"
ALERT_CONTACT_ID="${ALERT_CONTACT_ID:-}"
MONITOR_NAME="${MONITOR_NAME:-stone-server /health}"

if [[ -z "$API_KEY" || -z "$HEALTH_URL" ]]; then
    echo "ERROR: UPTIMEROBOT_API_KEY and HEALTH_URL must be set" >&2
    exit 1
fi

ALERT_CONTACTS_PARAM=""
if [[ -n "$ALERT_CONTACT_ID" ]]; then
    # Format: "<contact_id>_<threshold>_<recurrence>"
    ALERT_CONTACTS_PARAM="&alert_contacts=${ALERT_CONTACT_ID}_0_0"
fi

echo "Creating UptimeRobot monitor: ${MONITOR_NAME}"
echo "URL: ${HEALTH_URL}"

RESPONSE=$(curl -sf -X POST "https://api.uptimerobot.com/v2/newMonitor" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -H "Cache-Control: no-cache" \
    --data-urlencode "api_key=${API_KEY}" \
    --data-urlencode "friendly_name=${MONITOR_NAME}" \
    --data-urlencode "url=${HEALTH_URL}" \
    -d "type=1" \
    -d "interval=300" \
    -d "http_method=1" \
    -d "keyword_type=0" \
    -d "http_auth_type=0" \
    -d "format=json" \
    ${ALERT_CONTACTS_PARAM:+--data-urlencode "alert_contacts=${ALERT_CONTACT_ID}_0_0"})

STATUS=$(echo "$RESPONSE" | grep -o '"stat":"[^"]*"' | cut -d'"' -f4)

if [[ "$STATUS" == "ok" ]]; then
    MONITOR_ID=$(echo "$RESPONSE" | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)
    echo "Monitor created successfully (ID: ${MONITOR_ID})"
    echo "Dashboard: https://dashboard.uptimerobot.com/monitors/${MONITOR_ID}"
else
    echo "ERROR: Failed to create monitor" >&2
    echo "$RESPONSE" >&2
    exit 1
fi
