#!/bin/bash
set -eu
cd "$(dirname "$0")"
docker compose -f docker-compose.tester.yml down
echo "Server stopped. Data is preserved in Docker volumes."
echo "To wipe data too, run: docker compose -f docker-compose.tester.yml down -v"
