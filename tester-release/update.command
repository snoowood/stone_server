#!/bin/bash
set -eu
cd "$(dirname "$0")"

echo "=== Stone Server (tester) — updating ==="
echo

echo "[git] Pulling latest release files..."
if ! git pull --ff-only; then
  echo "[error] git pull failed. Resolve conflicts manually and re-run."
  read -n 1 -s -r -p "Press any key to close..."
  exit 1
fi

echo "[pull] Fetching the latest server image..."
docker compose -f docker-compose.tester.yml pull

echo "[up] Restarting services with the new image..."
docker compose -f docker-compose.tester.yml up -d

echo
echo "=== Update complete ==="
read -n 1 -s -r -p "Press any key to close..."
