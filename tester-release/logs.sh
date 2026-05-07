#!/bin/bash
set -eu
cd "$(dirname "$0")"
docker compose -f docker-compose.tester.yml logs --tail=200 "$@"
