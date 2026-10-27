#!/usr/bin/env bash
# ==============================================================================
# maintenance.sh
#
# Production maintenance, diagnostics, lifecycle control, Docker cleanup, and
# PostgreSQL optimization script for the Syn Discord Bot on AWS EC2.
# ==============================================================================

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
cd "$PROJECT_DIR"

COMMAND="${1:-status}"
if [[ $# -gt 0 && ! "$1" =~ ^- ]]; then
	shift
fi

TARGET="${1:-all}"
if [[ $# -gt 0 && ! "$1" =~ ^- ]]; then
	shift
fi

QUIET=0
while [[ $# -gt 0 ]]; do
	case "$1" in
		--quiet|-q)
			QUIET=1
			shift
			;;
		--help|-h)
			COMMAND="help"
			shift
			;;
		*)
			echo "Error: Unknown option '$1'. Run '$0 --help' for usage." >&2
			exit 1
			;;
	esac
done

log_info() {
	if [[ $QUIET -eq 0 ]]; then
		echo "$@"
	fi
}

# Load environment settings if present
DB_USER="syn_admin"
DB_NAME="syn_prod"
WEB_PORT="8080"

if [[ -f .env ]]; then
	val=$(grep -E '^DB_USER=' .env | cut -d '=' -f2- | tr -d "\"' " || true)
	[[ -n "$val" ]] && DB_USER="$val"
	val=$(grep -E '^DB_NAME=' .env | cut -d '=' -f2- | tr -d "\"' " || true)
	[[ -n "$val" ]] && DB_NAME="$val"
	val=$(grep -E '^WEB_PORT=' .env | cut -d '=' -f2- | tr -d "\"' " || true)
	[[ -n "$val" ]] && WEB_PORT="$val"
fi

case "$COMMAND" in
	status|health)
		echo "================================================================================"
		echo " 🩺 Syn EC2 & Docker Production Status ($(date -u '+%Y-%m-%d %H:%M:%S UTC'))"
		echo "================================================================================"

		# 1. Host EC2 Telemetry
		echo " 🖥️  EC2 Host Telemetry:"
		echo "    Uptime / Load: $(uptime | sed 's/^ *//')"
		echo "    CPU Cores:     $(nproc 2>/dev/null || echo '2')"
		if command -v free >/dev/null 2>&1; then
			mem_line=$(free -h | awk '/^Mem:/ {printf "Used %s / Total %s (Available: %s)", $3, $2, $7}')
			swap_line=$(free -h | awk '/^Swap:/ {printf "Used %s / Total %s", $3, $2}')
			echo "    Memory:        ${mem_line}"
			echo "    Swap:          ${swap_line}"
		fi
		disk_line=$(df -h / | awk 'NR==2 {printf "Used %s / %s (%s) on %s", $3, $2, $5, $6}')
		echo "    Root Disk:     ${disk_line}"
		echo ""

		# 2. Docker Containers
		echo " 🐳 Docker Containers:"
		if command -v docker >/dev/null 2>&1; then
			docker ps -a --filter "name=syn_" --format "    - {{.Names}}: {{.Status}} (Image: {{.Image}})"
			echo ""
			echo " 📊 Container Resource Utilization (Snapshot):"
			docker stats --no-stream --format "    - {{.Name}}: CPU={{.CPUPerc}} | MEM={{.MemUsage}} ({{.MemPerc}}) | PIDS={{.PIDs}}" syn_app syn_db 2>/dev/null || echo "    (Containers not running)"
		else
			echo "    ⚠️  Docker is not installed or not accessible."
		fi
		echo ""

		# 3. Web Server & Bot Health Endpoint
		echo " 🌐 Web Server & Health Probe (Port ${WEB_PORT}):"
		if health_resp=$(curl -fsS --max-time 3 "http://127.0.0.1:${WEB_PORT}/health" 2>/dev/null); then
			echo "    🟢 HTTP /health: ${health_resp}"
			echo "    🔗 Terms Page:   http://127.0.0.1:${WEB_PORT}/terms"
			echo "    🔗 Privacy Page: http://127.0.0.1:${WEB_PORT}/privacy"
		else
			echo "    🔴 HTTP /health on port ${WEB_PORT} is unreachable."
		fi
		echo ""

		# 4. Security Check: DBeaver Proxy Exposure
		if command -v docker >/dev/null 2>&1 && docker ps --format '{{.Names}}' | grep -q '^syn_db_dbeaver_proxy$'; then
			echo " ⚠️  SECURITY NOTICE: 'syn_db_dbeaver_proxy' is currently ACTIVE!"
			echo "    When finished with DBeaver, close external DB exposure with:"
			echo "    ./scripts/open_db_for_dbeaver.sh stop"
		else
			echo " 🔒 Database Isolation: Postgres is strictly isolated on internal Docker network."
		fi
		echo "================================================================================"
		;;

	restart)
		case "$TARGET" in
			bot|app)
				echo "🔄 Gracefully restarting syn_app (bot)..."
				docker compose restart bot
				;;
			db|postgres)
				echo "🔄 Gracefully restarting syn_db (postgres)..."
				docker compose restart postgres
				;;
			all|*)
				echo "🔄 Gracefully restarting all Syn services..."
				docker compose restart
				;;
		esac
		echo "✅ Restart complete."
		;;

	start)
		echo "▶️  Starting Syn services..."
		docker compose up -d
		echo "✅ Services started."
		;;

	stop)
		echo "🛑 Stopping Syn services..."
		docker compose stop
		echo "✅ Services stopped."
		;;

	prune|cleanup)
		log_info "🧹 Running EC2 & Docker disk cleanup..."
		if command -v docker >/dev/null 2>&1; then
			docker container prune -f >/dev/null 2>&1 || true
			docker image prune -f >/dev/null 2>&1 || true
			docker builder prune -f --keep-storage 500MB >/dev/null 2>&1 || true
			docker network prune -f >/dev/null 2>&1 || true
		fi
		# Clean up old rotated log archives older than 30 days
		if [[ -d logs ]]; then
			find logs -type f -name "*.gz" -mtime +30 -delete 2>/dev/null || true
		fi
		log_info "✅ Cleanup finished. Current disk usage: $(df -h / | awk 'NR==2 {print $3 " / " $2 " (" $5 ")"}')"
		;;

	vacuum)
		echo "🐘 Running PostgreSQL VACUUM (ANALYZE) on '${DB_NAME}'..."
		docker exec syn_db psql -U "$DB_USER" -d "$DB_NAME" -c "VACUUM (VERBOSE, ANALYZE);"
		echo "✅ PostgreSQL VACUUM ANALYZE completed."
		;;

	stats)
		echo "📈 Streaming live container metrics (Press Ctrl+C to exit)..."
		exec docker stats syn_app syn_db
		;;

	help|--help|-h|*)
		cat <<EOF
Usage: $0 <command> [target] [options]

EC2 and Docker maintenance toolkit for the Syn Discord Bot.

Commands:
  status | health        Show EC2 host telemetry, container stats, and /health status (Default)
  restart [all|bot|db]   Gracefully restart all services, only the bot, or only Postgres
  start                  Start Docker Compose services
  stop                   Stop Docker Compose services without removing volumes
  prune | cleanup        Reclaim EC2 disk space (prune dangling images, build cache, old logs)
  vacuum                 Run PostgreSQL VACUUM (ANALYZE) inside syn_db
  stats                  Stream live CPU/Memory metrics for syn_app and syn_db

Options:
  --quiet, -q            Suppress informational output (for cron jobs)
  --help, -h             Show this help message
EOF
		exit 0
		;;
esac

