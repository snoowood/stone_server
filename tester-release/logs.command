#!/bin/bash
set -eu
cd "$(dirname "$0")"
echo "=== Stone Server (tester) — last 200 log lines ==="
docker compose -f docker-compose.tester.yml logs --tail=200
echo
read -n 1 -s -r -p "Press any key to close..."
