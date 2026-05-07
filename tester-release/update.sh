#!/bin/bash
set -eu
cd "$(dirname "$0")"

echo "[git] Pulling latest release files..."
git pull --ff-only

echo "[pull] Fetching the latest server image..."
docker compose -f docker-compose.tester.yml pull

echo "[up] Restarting services with the new image..."
docker compose -f docker-compose.tester.yml up -d

echo "=== Update complete ==="
