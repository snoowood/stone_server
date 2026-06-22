#!/bin/bash
# Run the stone-server k6 load test.
#
# Profiles:
#   smoke    — 5 VUs × 1 min  (quick sanity check)
#   load     — 200 VUs × 3 min + 30s ramp  (CCU 200 review target)
#   stress   — 300 VUs × 3 min + 60s ramp  (above design capacity)
#
# Usage:
#   TARGET_URL=https://your-server ./scripts/load-test/run.sh [profile]
#
# Default target is https://localhost (matches Compose nginx port 443).
# Self-signed cert TLS verification is disabled inside the k6 scenario.
#
# DB pool + memory monitoring during the test:
#   Start the app with DIAG_INTERVAL_SECS=5, then:
#     docker compose logs -f app | grep '"message":"diag"'
#   pool_total_conns must stay <= 50; heap_alloc_bytes should recover after test.
#
# Requirements:
#   - k6 installed: https://k6.io/docs/get-started/installation/
#   - Server running with APP_ENV=development
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCENARIO="${SCRIPT_DIR}/scenario.js"

TARGET_URL="${TARGET_URL:-https://localhost}"
PROFILE="${1:-load}"

if ! command -v k6 &>/dev/null; then
    echo "ERROR: k6 is not installed." >&2
    echo "Install: https://k6.io/docs/get-started/installation/" >&2
    exit 1
fi

echo "Target:  ${TARGET_URL}"
echo "Profile: ${PROFILE}"
echo ""

case "$PROFILE" in
    smoke)
        VU_COUNT=5 RAMP_SECS=10 STEADY_SECS=60 \
            TARGET_URL="$TARGET_URL" \
            k6 run "$SCENARIO"
        ;;
    load)
        VU_COUNT=200 RAMP_SECS=30 STEADY_SECS=180 \
            TARGET_URL="$TARGET_URL" \
            k6 run "$SCENARIO"
        ;;
    stress)
        VU_COUNT=300 RAMP_SECS=60 STEADY_SECS=180 \
            TARGET_URL="$TARGET_URL" \
            k6 run "$SCENARIO"
        ;;
    *)
        echo "Unknown profile: $PROFILE" >&2
        echo "Available: smoke | load | stress" >&2
        exit 1
        ;;
esac
