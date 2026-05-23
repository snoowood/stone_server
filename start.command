#!/bin/bash
set -eu
cd "$(dirname "$0")"

echo "=== Stone Server (tester) — starting ==="
echo

if ! docker info >/dev/null 2>&1; then
  echo "[init] Docker Desktop is not running — trying to launch it..."
  if [ ! -d "/Applications/Docker.app" ]; then
    echo "[error] Docker Desktop is not installed."
    echo "        Install it from https://www.docker.com/products/docker-desktop/"
    read -n 1 -s -r -p "Press any key to close..."
    exit 1
  fi
  open -a Docker
  echo "[init] Waiting for Docker daemon (up to 90s)..."
  ready=0
  for i in $(seq 1 30); do
    if docker info >/dev/null 2>&1; then
      ready=1; break
    fi
    sleep 3
  done
  if [ "$ready" != "1" ]; then
    echo "[error] Docker did not become ready within 90 seconds."
    echo "        Launch Docker manually, wait for the whale icon to stop animating, then re-run."
    read -n 1 -s -r -p "Press any key to close..."
    exit 1
  fi
  echo "[init] Docker is ready."
fi

if [ ! -f .env ]; then
  echo "[init] First run — generating .env with random secrets..."
  docker run --rm \
    -v "$PWD:/work" -w /work \
    -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
    alpine:3.19 sh /work/_internal/init-env.sh
fi

echo "[pull] Fetching the latest server image..."
if ! docker compose -f docker-compose.tester.yml pull; then
  echo "[error] docker compose pull failed. Check your internet connection and try again."
  echo "        If this keeps happening, send logs.command output to the dev team."
  read -n 1 -s -r -p "Press any key to close..."
  exit 1
fi

echo "[up] Starting services..."
docker compose -f docker-compose.tester.yml up -d

HTTPS_PORT=8443
if [ -f .env ]; then
  HTTPS_PORT=$(grep '^HTTPS_PORT=' .env | cut -d= -f2)
  HTTPS_PORT=${HTTPS_PORT:-8443}
fi

echo "[wait] Waiting for the server to become healthy..."
for i in $(seq 1 30); do
  if curl -sfk "https://localhost:${HTTPS_PORT}/api/v1/health" >/dev/null 2>&1; then
    echo
    echo "=== Server ready ==="
    echo "  Health check : https://localhost:${HTTPS_PORT}/api/v1/health"
    echo "  Client URL   : https://localhost:${HTTPS_PORT}"
    echo
    echo "Use stop.command to shut down, logs.command to view logs."
    read -n 1 -s -r -p "Press any key to close..."
    exit 0
  fi
  sleep 2
done

echo "[error] Server did not become healthy within 60 seconds."
echo "        Run logs.command and send the output to the dev team."
read -n 1 -s -r -p "Press any key to close..."
exit 1
