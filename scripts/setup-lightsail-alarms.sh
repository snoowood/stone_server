#!/bin/bash
# Configure AWS Lightsail metric alarms for stone-server.
#
# Lightsail does not expose raw memory or TCP connection count metrics.
# The closest available proxies are used instead:
#   Memory pressure  → BurstCapacityPercentage (CPU burst credits; drops when
#                       the instance is under sustained CPU/memory pressure)
#   Connection load  → NetworkIn bytes (inbound traffic volume)
#
# Alarms created:
#   - CPUUtilization > 80% for 10 consecutive minutes    (CPU)
#   - BurstCapacityPercentage < 20% for 5 minutes        (memory/CPU pressure proxy)
#   - NetworkIn > 50 MB/min for 5 consecutive minutes    (connection load proxy)
#   - NetworkOut > 100 MB/min for 5 consecutive minutes  (egress spike / exfil)
#   - StatusCheckFailed ≥ 1 for 2 consecutive minutes    (instance health)
#
# Usage:
#   INSTANCE_NAME=stone-server \
#   NOTIFICATION_EMAIL=ops@yourdomain.com \
#   AWS_DEFAULT_REGION=ap-northeast-2 \
#   ./scripts/setup-lightsail-alarms.sh
#
# Prerequisites:
#   - AWS CLI v2 installed and configured (aws configure)
#   - The Lightsail instance must already exist
set -euo pipefail

INSTANCE_NAME="${INSTANCE_NAME:-stone-server}"
NOTIFICATION_EMAIL="${NOTIFICATION_EMAIL:-}"
AWS_REGION="${AWS_DEFAULT_REGION:-ap-northeast-2}"

if [[ -z "$NOTIFICATION_EMAIL" ]]; then
    echo "ERROR: NOTIFICATION_EMAIL is not set" >&2
    exit 1
fi

CONTACT_PROTOCOLS='["Email"]'
CONTACT_ENDPOINTS="[\"${NOTIFICATION_EMAIL}\"]"

alarm() {
    local name="$1" metric="$2" threshold="$3" periods="$4" op="$5" unit="$6"
    echo "→ Creating alarm: ${name}"
    aws lightsail put-alarm \
        --region "$AWS_REGION" \
        --alarm-name "$name" \
        --monitored-resource-name "$INSTANCE_NAME" \
        --metric-name "$metric" \
        --comparison-operator "$op" \
        --threshold "$threshold" \
        --evaluation-periods "$periods" \
        --datapoints-to-alarm "$periods" \
        --treat-missing-data "breaching" \
        --notification-enabled \
        --notification-triggers "ALARM" "OK" \
        --contact-protocols $CONTACT_PROTOCOLS \
        --notification-recipient-emails $CONTACT_ENDPOINTS
}

echo "Configuring Lightsail alarms for instance: ${INSTANCE_NAME} (region: ${AWS_REGION})"

# ── CPU ───────────────────────────────────────────────────────────────────────
# Alert when CPU stays above 80% for 10 consecutive 1-minute periods.
alarm "${INSTANCE_NAME}-cpu-high" \
    "CPUUtilization" 80 10 "GreaterThanThreshold" "Percent"

# ── Memory pressure proxy ─────────────────────────────────────────────────────
# Lightsail does not expose a raw memory metric. BurstCapacityPercentage
# (remaining CPU burst credit %) drops when the instance is under sustained
# load. Alert when it falls below 20% for 5 consecutive minutes.
alarm "${INSTANCE_NAME}-burst-capacity-low" \
    "BurstCapacityPercentage" 20 5 "LessThanThreshold" "Percent"

# ── Connection load proxy ─────────────────────────────────────────────────────
# Lightsail does not expose TCP connection count. Inbound bytes (NetworkIn)
# serve as a traffic-volume proxy. Alert on > 50 MB/min sustained for 5 min.
alarm "${INSTANCE_NAME}-network-in-high" \
    "NetworkIn" 52428800 5 "GreaterThanThreshold" "Bytes"

# ── Egress spike (abuse / data exfiltration) ──────────────────────────────────
alarm "${INSTANCE_NAME}-network-out-spike" \
    "NetworkOut" 104857600 5 "GreaterThanThreshold" "Bytes"

# ── Instance health ───────────────────────────────────────────────────────────
# Alert on 2 consecutive failed status checks (≈ 2 minutes).
alarm "${INSTANCE_NAME}-status-check" \
    "StatusCheckFailed" 0 2 "GreaterThanThreshold" "Count"

echo "Done. 5 alarms created for: ${INSTANCE_NAME}"
echo "Note: confirmation emails will be sent to ${NOTIFICATION_EMAIL} — confirm each subscription before alerts are delivered."
