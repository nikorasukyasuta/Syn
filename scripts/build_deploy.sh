#!/usr/bin/env bash
# ==============================================================================
# build_deploy.sh
#
# Production build and deployment script for the Syn Discord Bot on AWS EC2.
# Builds updated Docker images while current containers keep serving traffic,
# optionally creates a pre-deploy PostgreSQL backup, swaps containers with
# minimal downtime, and verifies container + HTTP /health readiness.
# ==============================================================================

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
cd "$PROJECT_DIR"

GIT_PULL=0
BACKUP_DB=0
NO_CACHE=0
BOT_ONLY=0
SKIP_HEALTH=0

while [[ $# -gt 0 ]]; do
	case "$1" in
		--pull|--git-pull|-p)
			GIT_PULL=1
			shift
			;;
		--backup|--backup-db|-b)
			BACKUP_DB=1
			shift
			;;
		--no-cache)
			NO_CACHE=1
			shift
			;;
		--bot-only|--zero-db-downtime)
			BOT_ONLY=1
			shift
			;;
		--skip-health)
			SKIP_HEALTH=1
			shift
			;;
		--help|-h)
			cat <<EOF
Usage: $0 [options]

Builds and deploys the Syn Discord Bot & PostgreSQL stack on EC2.

Options:
  --pull, -p        Run 'git pull --ff-only' before building
  --backup, -b      Create a PostgreSQL backup before deploying (via db_backup.sh)
  --bot-only        Rebuild and restart ONLY syn_app without stopping syn_db
  --no-cache        Build Docker images from scratch without layer cache
  --skip-health     Skip post-deployment HTTP /health verification
  --help, -h        Show this help message
EOF
			exit 0
			;;
		*)
			echo "Error: Unknown option '$1'. Run '$0 --help' for usage." >&2
			exit 1
			;;
	esac
done

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

echo "================================================================================"
echo " 🚀 Syn Production Deployment ($(date -u '+%Y-%m-%d %H:%M:%S UTC'))"
echo "================================================================================"

# 1. Optional Git Pull
if [[ $GIT_PULL -eq 1 ]]; then
	echo "📥 Pulling latest source code from Git..."
	git pull --ff-only
fi

if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo 'unknown')"
	CURRENT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
	echo "📌 Git Revision: ${CURRENT_BRANCH} @ ${CURRENT_COMMIT}"
fi

# 2. Ensure required host directories exist prior to build/mount
mkdir -p logs assets/panels config/filters backups

# 3. Optional Pre-Deployment Database Backup
if [[ $BACKUP_DB -eq 1 ]]; then
	if [[ -x "${SCRIPT_DIR}/db_backup.sh" ]]; then
		echo "💾 Creating pre-deployment PostgreSQL snapshot..."
		"${SCRIPT_DIR}/db_backup.sh" backup --prefix "pre_deploy"
	else
		echo "⚠️  db_backup.sh not found or not executable; skipping backup."
	fi
fi

# 4. Build new images while current containers remain running
build_args=()
if [[ $NO_CACHE -eq 1 ]]; then
	build_args+=(--no-cache)
fi
if [[ $BOT_ONLY -eq 1 ]]; then
	build_args+=(bot)
fi

echo "🔨 Building updated images; current containers will keep running..."
"${compose[@]}" build "${build_args[@]}"

# 5. Swap and start containers
if [[ $BOT_ONLY -eq 1 ]]; then
	echo "🔄 Recreating 'bot' (syn_app) container without stopping PostgreSQL..."
	"${compose[@]}" up --detach --no-build --no-deps bot
else
	echo "🛑 Build finished. Stopping and removing old containers..."
	"${compose[@]}" down

	echo "▶️  Starting services with newly built images..."
	"${compose[@]}" up --detach --no-build --remove-orphans
fi

# 6. Post-deployment readiness check
if [[ $SKIP_HEALTH -eq 0 ]]; then
	WEB_PORT="8080"
	if [[ -f .env ]]; then
		env_port=$(grep -E '^WEB_PORT=' .env | cut -d '=' -f2- | tr -d "\"' " || true)
		[[ -n "$env_port" ]] && WEB_PORT="$env_port"
	fi

	echo "🩺 Waiting for syn_app health check on port ${WEB_PORT}..."
	healthy=0
	for _ in {1..15}; do
		if curl -fsS --max-time 2 "http://127.0.0.1:${WEB_PORT}/health" >/dev/null 2>&1; then
			healthy=1
			break
		fi
		sleep 2
	done

	if [[ $healthy -eq 1 ]]; then
		health_json=$(curl -fsS --max-time 2 "http://127.0.0.1:${WEB_PORT}/health" 2>/dev/null || echo "{}")
		echo "✅ Health check passed: ${health_json}"
	else
		echo "⚠️  Warning: HTTP /health endpoint did not respond within 30s. Check './scripts/logs.sh --bot'."
	fi
fi

echo "================================================================================"
echo "✅ Deployment complete."
echo "   View live logs:   ./scripts/logs.sh"
echo "   Check status:     ./scripts/maintenance.sh status"
echo "================================================================================"
