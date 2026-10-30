#!/usr/bin/env bash
# ==============================================================================
# synctl.sh
#
# Unified master operations CLI for managing the Syn Discord Bot & PostgreSQL
# production stack on AWS EC2. Dispatches to the categorized helper scripts in
# ./scripts/{deploy,db,ops,logs}/.
# ==============================================================================

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

SUBCMD="${1:-help}"
if [[ $# -gt 0 ]]; then
	shift
fi

case "$SUBCMD" in
	deploy|build)
		exec "${SCRIPT_DIR}/deploy/build_deploy.sh" "$@"
		;;
	dev|local-dev)
		# Verify we are not running on an AWS EC2 instance before starting local dev mode
		for dmi_path in /sys/devices/virtual/dmi/id/sys_vendor /sys/devices/virtual/dmi/id/board_vendor /sys/devices/virtual/dmi/id/bios_vendor /sys/devices/virtual/dmi/id/product_name /sys/devices/virtual/dmi/id/chassis_asset_tag; do
			if [[ -r "$dmi_path" ]] && grep -qi "amazon ec2" "$dmi_path" 2>/dev/null; then
				echo "❌ Security Violation: 'synctl.sh dev' cannot be executed on an AWS EC2 instance." >&2
				exit 1
			fi
		done
		if [[ -f /var/lib/cloud/data/instance-id ]] && grep -qE '^i-[0-9a-f]+' /var/lib/cloud/data/instance-id 2>/dev/null; then
			echo "❌ Security Violation: 'synctl.sh dev' cannot be executed on an AWS EC2 instance." >&2
			exit 1
		fi
		PROJECT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
		cd "$PROJECT_DIR"
		echo "🧪 Starting Syn Bot in LOCAL DEVELOPMENT mode (APP_ENV=development)..."
		exec env APP_ENV=development go run ./cmd/bot/main.go -dev "$@"
		;;
	logs|log)
		exec "${SCRIPT_DIR}/logs/logs.sh" "$@"
		;;
	status|health)
		exec "${SCRIPT_DIR}/ops/maintenance.sh" status "$@"
		;;
	restart|start|stop|prune|cleanup|vacuum|stats)
		exec "${SCRIPT_DIR}/ops/maintenance.sh" "$SUBCMD" "$@"
		;;
	backup|restore|psql|shell|db-list|db-summary)
		case "$SUBCMD" in
			db-list)    exec "${SCRIPT_DIR}/db/db_backup.sh" list "$@" ;;
			db-summary) exec "${SCRIPT_DIR}/db/db_backup.sh" summary "$@" ;;
			*)          exec "${SCRIPT_DIR}/db/db_backup.sh" "$SUBCMD" "$@" ;;
		esac
		;;
	dbeaver)
		exec "${SCRIPT_DIR}/db/open_db_for_dbeaver.sh" "$@"
		;;
	setup|check|audit|init-config|swap|systemd|cron)
		if [[ "$SUBCMD" == "setup" ]]; then
			exec "${SCRIPT_DIR}/ops/ec2_setup.sh" "$@"
		else
			exec "${SCRIPT_DIR}/ops/ec2_setup.sh" "$SUBCMD" "$@"
		fi
		;;
	help|--help|-h|*)
		cat <<EOF
================================================================================
 🛠️  synctl — Unified EC2 Operations CLI for Syn
================================================================================
Usage: $0 <command> [options]

Deployment & Local Development:
  deploy [--pull] [--backup] [--bot-only]   Build & deploy latest containers (Production EC2)
  dev                                       Run bot locally in development mode (Blocked on EC2)

Logs & Telemetry (scripts/logs/):
  logs [--bot|--db|--file|--metrics]        Stream or filter logs
  logs --metrics-csv                        Export telemetry log as CSV
  logs --grep <pattern>                     Search logs by user/channel/keyword
  logs --export                             Bundle logs into a .tar.gz archive

Maintenance & Health (scripts/ops/):
  status | health                           Full EC2, Docker, and /health report
  restart [all|bot|db]                      Gracefully restart containers
  start | stop                              Start or stop the Docker stack
  stats                                     Live CPU & memory stream (2-core EC2)
  prune                                     Reclaim EC2 disk space & build cache
  vacuum                                    Run PostgreSQL VACUUM (ANALYZE)

Database & Backups (scripts/db/):
  backup                                    Create compressed PostgreSQL dump
  db-list                                   List available backups in ./backups/
  restore <file>                            Safely restore a database backup
  psql                                      Open interactive PostgreSQL shell
  db-summary                                Show row counts & sizes per table
  dbeaver [start|stop|status] [--ssh-only]  Manage remote DBeaver access

EC2 Host Setup & Hardening (scripts/ops/):
  check                                     Audit EC2 host, .env, Swap, & Docker
  init-config                               Scaffold .env and config files
  swap                                      Provision 2GB /swapfile (sudo)
  systemd                                   Enable auto-start on EC2 boot (sudo)
  cron                                      Enable daily backup & cleanup cron (sudo)
================================================================================
EOF
		exit 0
		;;
esac
