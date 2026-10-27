#!/usr/bin/env bash
# ==============================================================================
# scripts/logs/logs.sh
#
# Production log viewer, tailer, and analyzer for the Syn Discord Bot on EC2.
# Supports viewing Docker container stdout/stderr (syn_app & syn_db), the
# rotating JSON log file (./logs/syn.log), and the dedicated system resource
# telemetry log (./logs/metrics.jsonl) in JSONL or CSV format.
# ==============================================================================

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
cd "$PROJECT_DIR"

SOURCE="docker"          # docker | file | metrics | metrics-csv
TARGET="all"             # all | bot | db
FOLLOW=1                 # 1 = tail -f, 0 = snapshot
LINES=100
SINCE=""
ERRORS_ONLY=0
GREP_PATTERN=""
RAW_OUTPUT=0
EXPORT_BUNDLE=0

while [[ $# -gt 0 ]]; do
	case "$1" in
		--bot|-b)
			TARGET="bot"
			shift
			;;
		--db|-d)
			TARGET="db"
			shift
			;;
		--file|-f)
			SOURCE="file"
			shift
			;;
		--metrics|-m)
			SOURCE="metrics"
			shift
			;;
		--metrics-csv)
			SOURCE="metrics-csv"
			FOLLOW=0
			shift
			;;
		--no-follow|--tail)
			FOLLOW=0
			shift
			;;
		--lines|-n)
			LINES="${2:-100}"
			shift 2
			;;
		--since|-s)
			SINCE="${2:-1h}"
			shift 2
			;;
		--errors|-e)
			ERRORS_ONLY=1
			shift
			;;
		--grep|-g)
			GREP_PATTERN="${2:-}"
			shift 2
			;;
		--raw|-r)
			RAW_OUTPUT=1
			shift
			;;
		--export)
			EXPORT_BUNDLE=1
			shift
			;;
		--help|-h)
			cat <<EOF
Usage: $0 [options]

View, filter, or export Syn Discord Bot, PostgreSQL, and Resource Telemetry logs on EC2.

Log Sources & Targets:
  --bot, -b              Show only Discord bot container logs (syn_app)
  --db, -d               Show only PostgreSQL container logs (syn_db)
  --file, -f             Read directly from the rotating disk log (./logs/syn.log)
  --metrics, -m          Read from the separate resource telemetry log (./logs/metrics.jsonl)
  --metrics-csv          Output resource telemetry log (./logs/metrics.jsonl) as CSV for graphing

Filtering & Display:
  --lines, -n <NUM>      Number of historical lines to display (Default: 100)
  --no-follow            Print recent lines and exit instead of live streaming
  --since, -s <DURATION> Show Docker logs since duration (e.g., 15m, 2h, 2026-10-27T00:00:00)
  --errors, -e           Show only WARN and ERROR level log entries
  --grep, -g <PATTERN>   Filter logs by case-insensitive keyword, user ID, channel ID, etc.
  --raw, -r              Disable jq formatting even if jq is installed

Incident Export:
  --export               Package ./logs/ and recent Docker logs into a .tar.gz bundle
EOF
			exit 0
			;;
		*)
			echo "Error: Unknown option '$1'. Run '$0 --help' for usage." >&2
			exit 1
			;;
	esac
done

LOG_FILE="logs/syn.log"
if [[ -f .env ]]; then
	env_log=$(grep -E '^LOG_FILE_PATH=' .env | cut -d '=' -f2- | tr -d "\"' " || true)
	[[ -n "$env_log" ]] && LOG_FILE="$env_log"
fi

if [[ $EXPORT_BUNDLE -eq 1 ]]; then
	mkdir -p backups
	TIMESTAMP="$(date -u '+%Y%m%d_%H%M%S')"
	BUNDLE_PATH="backups/syn_logs_${TIMESTAMP}.tar.gz"
	TMP_DIR="$(mktemp -d)"
	trap 'rm -rf "$TMP_DIR"' EXIT

	echo "📦 Collecting logs into incident bundle..."
	if [[ -d logs ]]; then
		cp -r logs "$TMP_DIR/disk_logs" 2>/dev/null || true
	fi
	if command -v docker >/dev/null 2>&1; then
		docker logs --tail 5000 syn_app > "$TMP_DIR/docker_syn_app.log" 2>&1 || true
		docker logs --tail 2000 syn_db > "$TMP_DIR/docker_syn_db.log" 2>&1 || true
		docker ps -a > "$TMP_DIR/docker_ps.txt" 2>&1 || true
	fi

	tar -czf "$BUNDLE_PATH" -C "$TMP_DIR" .
	echo "✅ Log archive created: ${BUNDLE_PATH} ($(du -h "$BUNDLE_PATH" | awk '{print $1}'))"
	exit 0
fi

filter_stream() {
	local cmd_chain="cat"

	if [[ $ERRORS_ONLY -eq 1 ]]; then
		cmd_chain="$cmd_chain | grep -iE '\"level\":\"(ERROR|WARN)\"|level=(ERROR|WARN)|FATAL|PANIC|ERROR:'"
	fi

	if [[ -n "$GREP_PATTERN" ]]; then
		cmd_chain="$cmd_chain | grep -i --line-buffered $(printf '%q' "$GREP_PATTERN")"
	fi

	if [[ $RAW_OUTPUT -eq 0 && "$SOURCE" == "file" ]] && command -v jq >/dev/null 2>&1; then
		cmd_chain="$cmd_chain | jq -Rr 'fromjson? // .'"
	fi

	eval "$cmd_chain"
}

if [[ "$SOURCE" == "file" ]]; then
	if [[ ! -f "$LOG_FILE" ]]; then
		echo "Error: Log file '$LOG_FILE' does not exist yet." >&2
		exit 1
	fi

	echo "📄 Reading disk log file: $LOG_FILE (lines=$LINES, follow=$FOLLOW)"
	echo "--------------------------------------------------------------------------------"
	if [[ $FOLLOW -eq 1 ]]; then
		tail -n "$LINES" -f "$LOG_FILE" | filter_stream
	else
		tail -n "$LINES" "$LOG_FILE" | filter_stream
	fi
	exit 0
fi

METRICS_FILE="logs/metrics.jsonl"
if [[ "$SOURCE" == "metrics" ]]; then
	if [[ ! -f "$METRICS_FILE" ]]; then
		echo "Error: Metrics log file '$METRICS_FILE' does not exist yet." >&2
		exit 1
	fi

	echo "📈 Reading resource telemetry log: $METRICS_FILE (lines=$LINES, follow=$FOLLOW)"
	echo "--------------------------------------------------------------------------------"
	if [[ $FOLLOW -eq 1 ]]; then
		tail -n "$LINES" -f "$METRICS_FILE"
	else
		tail -n "$LINES" "$METRICS_FILE"
	fi
	exit 0
fi

if [[ "$SOURCE" == "metrics-csv" ]]; then
	if [[ ! -f "$METRICS_FILE" ]]; then
		echo "Error: Metrics log file '$METRICS_FILE' does not exist yet." >&2
		exit 1
	fi
	if ! command -v jq >/dev/null 2>&1; then
		echo "Error: 'jq' is required to convert metrics.jsonl to CSV." >&2
		exit 1
	fi
	echo "timestamp,epoch_ms,host_cpu_pct,monitor_cpu_pct,load_avg_1m,host_mem_used_mb,host_mem_pct,go_heap_alloc_mb,go_goroutines,db_acquired_conns,db_total_conns,queue_depth,queue_processed_total"
	jq -r '[.timestamp, .epoch_ms, .host_cpu_pct, .monitor_cpu_pct, .load_avg_1m, .host_mem_used_mb, .host_mem_pct, .go_heap_alloc_mb, .go_goroutines, .db_acquired_conns, .db_total_conns, .queue_depth, .queue_processed_total] | @csv' "$METRICS_FILE"
	exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
	echo "Error: Docker is not installed. Use '$0 --file' to read disk logs." >&2
	exit 1
fi

docker_args=(compose logs --tail "$LINES" --timestamps)
if [[ $FOLLOW -eq 1 ]]; then
	docker_args+=(--follow)
fi
if [[ -n "$SINCE" ]]; then
	docker_args+=(--since "$SINCE")
fi

case "$TARGET" in
	bot)
		docker_args+=(bot)
		;;
	db)
		docker_args+=(postgres)
		;;
	all)
		;;
esac

if [[ $ERRORS_ONLY -eq 1 || -n "$GREP_PATTERN" ]]; then
	docker "${docker_args[@]}" 2>&1 | filter_stream
else
	docker "${docker_args[@]}"
fi

