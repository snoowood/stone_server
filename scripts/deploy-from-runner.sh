#!/usr/bin/env bash
set -euo pipefail

BASE_DIR="/opt/snoowoo"
APP_DIR="$BASE_DIR/stone-server"
ENV_FILE="$APP_DIR/runtime.env"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required command: $1" >&2
    exit 1
  }
}

read_secret() {
  local secret_id="$1"
  oci secrets secret-bundle get --auth instance_principal --secret-id "$secret_id" \
    --query 'data."secret-bundle-content".content' \
    --raw-output | base64 --decode
}

env_value() {
  local key="$1"
  awk -F= -v key="$key" '$1 == key { sub(/^[^=]*=/, ""); print }' "$ENV_FILE" | tail -n 1
}

require_cmd base64
require_cmd docker
require_cmd oci

if [[ -z "${STONE_SERVER_ENV_SECRET_ID:-}" ]]; then
  echo "STONE_SERVER_ENV_SECRET_ID is not set. Add it to the stone_server GitHub repository secrets." >&2
  exit 1
fi

mkdir -p "$APP_DIR/postgres" "$APP_DIR/redis"
cp deploy/compose.yaml "$APP_DIR/compose.yaml"
rm -f "$APP_DIR/.env"
install -m 0600 /dev/null "$ENV_FILE"
read_secret "$STONE_SERVER_ENV_SECRET_ID" > "$ENV_FILE"

if [[ -z "$(env_value DB_PASSWORD)" ]]; then
  echo "DB_PASSWORD is required in the stone-server env secret." >&2
  exit 1
fi
if [[ -z "$(env_value JWT_PRIVATE_KEY)" ]]; then
  echo "JWT_PRIVATE_KEY is required in the stone-server env secret." >&2
  exit 1
fi
if [[ -z "$(env_value STEAM_API_KEY)" || -z "$(env_value STEAM_APP_ID)" ]]; then
  echo "STEAM_API_KEY and STEAM_APP_ID are required in production." >&2
  exit 1
fi

ghcr_token="${GHCR_TOKEN:-$(env_value GHCR_TOKEN)}"
if [[ -n "$ghcr_token" ]]; then
  ghcr_user="${GHCR_USER:-snoowood}"
  printf '%s' "$ghcr_token" | docker login ghcr.io -u "$ghcr_user" --password-stdin
fi

docker network inspect snoowoo-public >/dev/null 2>&1 || docker network create snoowoo-public
docker compose --env-file "$ENV_FILE" -f "$APP_DIR/compose.yaml" pull
docker compose --env-file "$ENV_FILE" -f "$APP_DIR/compose.yaml" up -d
docker image prune -f >/dev/null

for container in stone-postgres stone-redis stone-server; do
  for attempt in {1..30}; do
    if [[ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null || true)" == "true" ]]; then
      break
    fi
    sleep 2
  done

  if [[ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null || true)" != "true" ]]; then
    docker compose --env-file "$ENV_FILE" -f "$APP_DIR/compose.yaml" ps >&2 || true
    docker logs --tail=120 "$container" >&2 || true
    echo "$container failed to start after 60 seconds." >&2
    exit 1
  fi
done

for attempt in {1..30}; do
  if docker exec stone-server wget -qO- http://127.0.0.1:8080/api/v1/health >/dev/null 2>&1; then
    exit 0
  fi
  sleep 2
done

docker compose --env-file "$ENV_FILE" -f "$APP_DIR/compose.yaml" ps >&2 || true
docker logs --tail=120 stone-server >&2 || true
echo "Stone server health check failed after 60 seconds." >&2
exit 1
