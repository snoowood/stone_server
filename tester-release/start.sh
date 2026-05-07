#!/bin/bash
set -eu
cd "$(dirname "$0")"

echo "=== Stone Server (tester) — starting ==="

if ! docker info >/dev/null 2>&1; then
  echo "[error] Docker is not running or the current user is not in the docker group."
  exit 1
fi

if [ ! -f .env ]; then
  echo "[init] First run — generating .env with random secrets..."
  docker run --rm \
    -v "$PWD:/work" -w /work \
    -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
    alpine:3.19 sh /work/_internal/init-env.sh
fi

echo "[pull] Fetching the latest server image..."
docker compose -f docker-compose.tester.yml pull

echo "[up] Starting services..."
docker compose -f docker-compose.tester.yml up -d

echo "[wait] Waiting for the server to become healthy..."
for i in $(seq 1 30); do
  if curl -sfk https://localhost:8443/api/v1/health >/dev/null 2>&1; then
    echo "=== Server ready ==="
    echo "  Health check : https://localhost:8443/api/v1/health"
    echo "  Client URL   : https://localhost:8443"
    exit 0
  fi
  sleep 2
done

echo "[error] Server did not become healthy within 60 seconds. Check ./logs.sh"
exit 1
