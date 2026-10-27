#!/usr/bin/env bash
# ==============================================================================
# scripts/db/db_backup.sh
#
# PostgreSQL backup, restore, interactive psql shell, and table summary helper
# for the containerized Syn database (syn_db) on AWS EC2.
# ==============================================================================

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
cd "$PROJECT_DIR"

DB_CONTAINER="syn_db"
BACKUP_DIR="${PROJECT_DIR}/backups"
RETENTION_DAYS=14
PREFIX="syn_db"
QUIET=0
AUTO_YES=0

COMMAND="${1:-backup}"
if [[ $# -gt 0 && ! "$1" =~ ^- ]]; then
	shift
fi

RESTORE_FILE=""
if [[ "$COMMAND" == "restore" && $# -gt 0 && ! "$1" =~ ^- ]]; then
	RESTORE_FILE="$1"
	shift
fi

while [[ $# -gt 0 ]]; do
	case "$1" in
		--retention|-r)
			RETENTION_DAYS="${2:-14}"
			shift 2
			;;
		--prefix|-p)
			PREFIX="${2:-syn_db}"
			shift 2
			;;
		--quiet|-q)
			QUIET=1
			shift
			;;
		--yes|-y)
			AUTO_YES=1
			shift
			;;
		--help|-h)
			cat <<EOF
Usage: $0 <command> [options]

Manage backups, restores, and interactive SQL sessions for the Syn PostgreSQL container.

Commands:
  backup               Create a gzip-compressed SQL dump in ./backups/ (Default)
  list                 List available backups in ./backups/ with sizes and dates
  restore <file>       Restore database from a .sql.gz or .sql backup file
  psql | shell         Open an interactive psql console inside syn_db
  summary              Print row counts for all Syn production tables

Options:
  --retention, -r <N>  Delete backups older than N days (Default: 14)
  --prefix, -p <NAME>  Filename prefix for backup archive (Default: syn_db)
  --yes, -y            Skip interactive confirmation prompt during restore
  --quiet, -q          Minimal output (ideal for cron jobs)
  --help, -h           Show this help message
EOF
			exit 0
			;;
		*)
			echo "Error: Unknown argument '$1'. Run '$0 --help' for usage." >&2
			exit 1
			;;
	esac
done

# Load database credentials from .env
DB_USER="syn_admin"
DB_NAME="syn_prod"

if [[ -f .env ]]; then
	val=$(grep -E '^DB_USER=' .env | cut -d '=' -f2- | tr -d "\"' " || true)
	[[ -n "$val" ]] && DB_USER="$val"

	val=$(grep -E '^DB_NAME=' .env | cut -d '=' -f2- | tr -d "\"' " || true)
	[[ -n "$val" ]] && DB_NAME="$val"
fi

log_info() {
	if [[ $QUIET -eq 0 ]]; then
		echo "$@"
	fi
}

ensure_db_running() {
	if ! docker ps --format '{{.Names}}' | grep -q "^${DB_CONTAINER}$"; then
		echo "Error: PostgreSQL container '${DB_CONTAINER}' is not running." >&2
		exit 1
	fi
}

mkdir -p "$BACKUP_DIR"

case "$COMMAND" in
	backup)
		ensure_db_running
		TIMESTAMP="$(date -u '+%Y%m%d_%H%M%S')"
		OUT_FILE="${BACKUP_DIR}/${PREFIX}_${TIMESTAMP}.sql.gz"

		log_info "💾 Creating PostgreSQL backup of '${DB_NAME}' (user: ${DB_USER})..."
		docker exec "$DB_CONTAINER" pg_dump -U "$DB_USER" -d "$DB_NAME" --clean --if-exists --no-owner --no-privileges | gzip -9 > "$OUT_FILE"

		if ! gzip -t "$OUT_FILE" 2>/dev/null; then
			echo "Error: Backup archive failed gzip integrity check: $OUT_FILE" >&2
			rm -f "$OUT_FILE"
			exit 1
		fi

		SIZE="$(du -h "$OUT_FILE" | awk '{print $1}')"
		log_info "✅ Backup saved: ${OUT_FILE} (${SIZE})"

		if [[ "$RETENTION_DAYS" -gt 0 ]]; then
			find "$BACKUP_DIR" -maxdepth 1 -name "${PREFIX}_*.sql.gz" -type f -mtime +"$RETENTION_DAYS" -delete 2>/dev/null || true
			log_info "🧹 Pruned '${PREFIX}_*.sql.gz' backups older than ${RETENTION_DAYS} days."
		fi
		;;

	list)
		echo "================================================================================"
		echo " 📂 Available PostgreSQL Backups in ${BACKUP_DIR}"
		echo "================================================================================"
		if compgen -G "${BACKUP_DIR}/*.sql*" >/dev/null 2>&1; then
			ls -lh --time-style=long-iso "${BACKUP_DIR}"/*.sql* | awk '{printf "  %-48s %8s   %s %s\n", $8, $5, $6, $7}'
		else
			echo "  (No backup files found in ${BACKUP_DIR})"
		fi
		echo "================================================================================"
		;;

	restore)
		ensure_db_running
		if [[ -z "$RESTORE_FILE" ]]; then
			echo "Error: Please specify a backup file to restore. Usage: $0 restore <path/to/backup.sql.gz>" >&2
			exit 1
		fi
		if [[ ! -f "$RESTORE_FILE" ]]; then
			echo "Error: Backup file not found: $RESTORE_FILE" >&2
			exit 1
		fi

		if [[ $AUTO_YES -eq 0 ]]; then
			echo "⚠️  WARNING: This will overwrite tables in database '${DB_NAME}' with '${RESTORE_FILE}'."
			read -r -p "Type 'RESTORE' to confirm: " confirm
			if [[ "$confirm" != "RESTORE" ]]; then
				echo "Restore cancelled."
				exit 0
			fi
		fi

		SAFETY_FILE="${BACKUP_DIR}/pre_restore_$(date -u '+%Y%m%d_%H%M%S').sql.gz"
		echo "🛡️  Creating pre-restore safety snapshot at ${SAFETY_FILE}..."
		docker exec "$DB_CONTAINER" pg_dump -U "$DB_USER" -d "$DB_NAME" --clean --if-exists --no-owner --no-privileges | gzip -9 > "$SAFETY_FILE"

		echo "🔄 Restoring '${RESTORE_FILE}' into '${DB_NAME}'..."
		if [[ "$RESTORE_FILE" == *.gz ]]; then
			gunzip -c "$RESTORE_FILE" | docker exec -i "$DB_CONTAINER" psql -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME"
		else
			docker exec -i "$DB_CONTAINER" psql -v ON_ERROR_STOP=1 -U "$DB_USER" -d "$DB_NAME" < "$RESTORE_FILE"
		fi
		echo "✅ Database restore completed successfully."
		;;

	psql|shell)
		ensure_db_running
		echo "🐘 Connecting to PostgreSQL (${DB_NAME}) inside ${DB_CONTAINER}..."
		exec docker exec -it "$DB_CONTAINER" psql -U "$DB_USER" -d "$DB_NAME"
		;;

	summary)
		ensure_db_running
		echo "================================================================================"
		echo " 📊 Syn PostgreSQL Table Summary (${DB_NAME})"
		echo "================================================================================"
		docker exec "$DB_CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -c "
SELECT
    relname AS table_name,
    n_live_tup AS approximate_rows,
    pg_size_pretty(pg_total_relation_size(relid)) AS total_size
FROM pg_stat_user_tables
ORDER BY relname;
"
		;;

	*)
		echo "Error: Unknown command '$COMMAND'. Run '$0 --help' for usage." >&2
		exit 1
		;;
esac

