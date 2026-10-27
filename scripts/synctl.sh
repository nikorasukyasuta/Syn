#!/usr/bin/env bash
# ==============================================================================
# synctl.sh
#
# Unified master operations CLI for managing the Syn Discord Bot & PostgreSQL
# production stack on AWS EC2. Dispatches to the specialized helper scripts in
# ./scripts/.
# ==============================================================================

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

SUBCMD="${1:-help}"
if [[ $# -gt 0 ]]; then
	shift
fi

case "$SUBCMD" in
	deploy|build)
		exec "${SCRIPT_DIR}/build_deploy.sh" "$@"
		;;
	logs|log)
		exec "${SCRIPT_DIR}/logs.sh" "$@"
		;;
	status|health)
		exec "${SCRIPT_DIR}/maintenance.sh" status "$@"
		;;
	restart|start|stop|prune|cleanup|vacuum|stats)
		exec "${SCRIPT_DIR}/maintenance.sh" "$SUBCMD" "$@"
		;;
	backup|restore|psql|shell|db-list|db-summary)
		case "$SUBCMD" in
			db-list)    exec "${SCRIPT_DIR}/db_backup.sh" list "$@" ;;
			db-summary) exec "${SCRIPT_DIR}/db_backup.sh" summary "$@" ;;
			*)          exec "${SCRIPT_DIR}/db_backup.sh" "$SUBCMD" "$@" ;;
		esac
		;;
	dbeaver)
		exec "${SCRIPT_DIR}/open_db_for_dbeaver.sh" "$@"
		;;
	setup|check|audit|init-config|swap|systemd|cron)
		if [[ "$SUBCMD" == "setup" ]]; then
			exec "${SCRIPT_DIR}/ec2_setup.sh" "$@"
		else
			exec "${SCRIPT_DIR}/ec2_setup.sh" "$SUBCMD" "$@"
		fi
		;;
	help|--help|-h|*)
		cat <<EOF
================================================================================
 🛠️  synctl — Unified EC2 Operations CLI for Syn
================================================================================
Usage: $0 <command> [options]

Deployment:
  deploy [--pull] [--backup] [--bot-only]   Build & deploy latest containers

Logs & Auditing:
  logs [--bot|--db|--file] [--errors]       Stream or filter logs
  logs --grep <pattern>                     Search logs by user/channel/keyword
  logs --export                             Bundle logs into a .tar.gz archive

Maintenance & Health:
  status | health                           Full EC2, Docker, and /health report
  restart [all|bot|db]                      Gracefully restart containers
  start | stop                              Start or stop the Docker stack
  stats                                     Live CPU & memory stream (2-core EC2)
  prune                                     Reclaim EC2 disk space & build cache
  vacuum                                    Run PostgreSQL VACUUM (ANALYZE)

Database & Backups:
  backup                                    Create compressed PostgreSQL dump
  db-list                                   List available backups in ./backups/
  restore <file>                            Safely restore a database backup
  psql                                      Open interactive PostgreSQL shell
  db-summary                                Show row counts & sizes per table
  dbeaver [start|stop|status] [--ssh-only]  Manage remote DBeaver access

EC2 Host Setup & Hardening:
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

