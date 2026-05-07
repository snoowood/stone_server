#!/bin/bash
set -eu
cd "$(dirname "$0")"
echo "=== Stone Server (tester) — stopping ==="
docker compose -f docker-compose.tester.yml down
echo
echo "Server stopped. Data is preserved in Docker volumes."
echo "To wipe data too, run: docker compose -f docker-compose.tester.yml down -v"
read -n 1 -s -r -p "Press any key to close..."
