#!/usr/bin/env bash
set -Eeuo pipefail

# Build new images before stopping the currently running services.
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
cd "$PROJECT_DIR"

if ! command -v docker >/dev/null 2>&1; then
	echo "Error: Docker is not installed or not on PATH." >&2
	exit 1
fi

compose=(docker compose)
if [[ -n "${COMPOSE_FILE:-}" ]]; then
	compose+=(--file "$COMPOSE_FILE")
elif [[ -f compose.yaml ]]; then
	compose+=(--file compose.yaml)
elif [[ -f compose.yml ]]; then
	compose+=(--file compose.yml)
elif [[ -f docker-compose.yaml ]]; then
	compose+=(--file docker-compose.yaml)
elif [[ -f docker-compose.yml ]]; then
	compose+=(--file docker-compose.yml)
else
	echo "Error: No Compose file found in $PROJECT_DIR. Set COMPOSE_FILE to its path." >&2
	exit 1
fi

echo "Building updated images; current containers will keep running..."
"${compose[@]}" build

echo "Build finished. Stopping and removing the old containers..."
"${compose[@]}" down

echo "Starting services with the newly built images..."
"${compose[@]}" up --detach --no-build --remove-orphans

echo "Deployment complete."
