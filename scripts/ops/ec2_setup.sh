#!/usr/bin/env bash
# ==============================================================================
# scripts/ops/ec2_setup.sh
#
# Production AWS EC2 host preparation, configuration auditor, swap file setup,
# systemd boot-service installer, and automated cron job installer for Syn.
# ==============================================================================

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
cd "$PROJECT_DIR"

COMMAND="${1:-check}"
if [[ $# -gt 0 ]]; then
	shift
fi

detect_public_ip() {
	local ip=""
	ip=$(curl -s --connect-timeout 2 http://169.254.169.254/latest/meta-data/public-ipv4 2>/dev/null || true)
	if [[ -z "$ip" ]]; then
		ip=$(curl -s --connect-timeout 2 https://ifconfig.me 2>/dev/null || true)
	fi
	if [[ -z "$ip" ]]; then
		ip=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
	fi
	echo "${ip:-<EC2_PUBLIC_IP>}"
}

case "$COMMAND" in
	check|audit)
		echo "================================================================================"
		echo " 🔍 Syn EC2 Production Readiness Audit"
		echo "================================================================================"
		PUB_IP="$(detect_public_ip)"
		echo " 🌐 Detected Host / EC2 IP: ${PUB_IP}"

		if command -v docker >/dev/null 2>&1; then
			echo " ✅ Docker installed: $(docker --version)"
		else
			echo " ❌ Docker is NOT installed."
		fi

		if docker compose version >/dev/null 2>&1; then
			echo " ✅ Docker Compose v2 installed: $(docker compose version --short 2>/dev/null || echo 'available')"
		else
			echo " ❌ Docker Compose v2 plugin is NOT installed."
		fi

		CORES="$(nproc 2>/dev/null || echo 'unknown')"
		echo " ✅ Host CPU Cores: ${CORES} (docker-compose configured for 2.0 CPUs)"
		SWAP_TOTAL="$(free -m 2>/dev/null | awk '/^Swap:/ {print $2}' || echo '0')"
		if [[ "${SWAP_TOTAL:-0}" -gt 0 ]]; then
			echo " ✅ Swap Space Active: ${SWAP_TOTAL} MB"
		else
			echo " ⚠️  No Swap Space active (Run '$0 swap' as root to add 2GB swap and prevent OOM during builds)"
		fi

		if [[ -f .env ]]; then
			perms=$(stat -c '%a' .env 2>/dev/null || echo "unknown")
			if [[ "$perms" == "600" || "$perms" == "400" ]]; then
				echo " ✅ .env file present with restricted permissions (${perms})"
			else
				echo " ⚠️  .env file permissions are ${perms} (Recommended: chmod 600 .env)"
			fi

			if grep -qE '^WEB_BASE_URL=http://localhost' .env 2>/dev/null || ! grep -qE '^WEB_BASE_URL=' .env 2>/dev/null; then
				echo " ℹ️  Tip: Set WEB_BASE_URL=http://${PUB_IP}:8080 in .env so /terms and /privacy commands link to your EC2 public IP."
			else
				echo " ✅ WEB_BASE_URL is configured in .env"
			fi
		else
			echo " ❌ .env file is missing! Run '$0 init-config' to generate a template."
		fi

		if [[ -f config/discord_ids.json ]]; then
			echo " ✅ config/discord_ids.json is present"
		else
			echo " ⚠️  config/discord_ids.json not found (using environment variables or run '$0 init-config')"
		fi

		if [[ -f config/filters/server.json ]]; then
			echo " ✅ config/filters/server.json is present"
		else
			echo " ⚠️  config/filters/server.json not found (run '$0 init-config' to scaffold)"
		fi
		echo "================================================================================"
		;;

	init-config)
		echo "📁 Initializing required directories and configuration templates..."
		mkdir -p logs backups assets/panels config/filters

		if [[ ! -f config/discord_ids.json && -f config/discord_ids.example.json ]]; then
			cp config/discord_ids.example.json config/discord_ids.json
			echo " ✅ Created config/discord_ids.json from config/discord_ids.example.json"
		fi

		if [[ ! -f config/filters/server.json ]]; then
			cat > config/filters/server.json <<'EOF'
{
  "words": []
}
EOF
			echo " ✅ Created default config/filters/server.json"
		fi

		if [[ ! -f .env ]]; then
			PUB_IP="$(detect_public_ip)"
			cat > .env <<EOF
# Syn Production Environment Secrets (.env)
# Only secrets and host connection endpoints belong here.
DISCORD_TOKEN=
DB_USER=syn_admin
DB_PASSWORD=change_me_strong_password
DB_HOST=postgres
DB_PORT=5432
DB_NAME=syn_prod
WEB_BASE_URL=http://${PUB_IP}:8080
EOF
			chmod 600 .env
			echo " ✅ Created minimal .env template with chmod 600 (Please fill in DISCORD_TOKEN and DB_PASSWORD)"
		else
			chmod 600 .env
			echo " ✅ Verified chmod 600 on existing .env"
		fi
		;;

	swap)
		if [[ $EUID -ne 0 ]]; then
			echo "Error: Creating a swapfile requires root privileges. Run: sudo $0 swap" >&2
			exit 1
		fi
		if swapon --show | grep -q '/swapfile'; then
			echo "✅ /swapfile is already active."
			exit 0
		fi
		echo "🔧 Provisioning 2GB /swapfile to protect 2-core EC2 instance against OOM during builds..."
		fallocate -l 2G /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=2048
		chmod 600 /swapfile
		mkswap /swapfile
		swapon /swapfile
		if ! grep -q '/swapfile' /etc/fstab; then
			echo '/swapfile none swap sw 0 0' >> /etc/fstab
		fi
		echo "✅ 2GB Swap enabled and persisted in /etc/fstab."
		;;

	systemd)
		if [[ $EUID -ne 0 ]]; then
			echo "Error: Installing a systemd unit requires root privileges. Run: sudo $0 systemd" >&2
			exit 1
		fi
		SERVICE_FILE="/etc/systemd/system/syn-bot.service"
		echo "⚙️  Installing systemd unit at ${SERVICE_FILE}..."
		cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Syn Discord Bot & PostgreSQL Docker Compose Stack
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=${PROJECT_DIR}
ExecStart=/usr/bin/docker compose up --detach --remove-orphans
ExecStop=/usr/bin/docker compose stop
TimeoutStartSec=0

[Install]
WantedBy=multi-user.target
EOF
		systemctl daemon-reload
		systemctl enable syn-bot.service
		echo "✅ systemd service 'syn-bot.service' enabled. Stack will automatically start on EC2 boot."
		;;

	cron)
		if [[ $EUID -ne 0 ]]; then
			echo "Error: Installing /etc/cron.d/ jobs requires root privileges. Run: sudo $0 cron" >&2
			exit 1
		fi
		CRON_FILE="/etc/cron.d/syn-maintenance"
		echo "⏰ Installing automated backup and cleanup schedule at ${CRON_FILE}..."
		cat > "$CRON_FILE" <<EOF
# Automated Syn Production Maintenance on EC2
SHELL=/bin/bash
PATH=/usr/local/sbin:/usr/local/bin:/sbin:/bin:/usr/sbin:/usr/bin

# Daily PostgreSQL compressed backup at 03:00 UTC (14-day retention)
0 3 * * * root ${PROJECT_DIR}/scripts/db/db_backup.sh backup --quiet >> ${PROJECT_DIR}/logs/cron_backup.log 2>&1

# Weekly Docker dangling image & build cache cleanup every Sunday at 04:00 UTC
0 4 * * 0 root ${PROJECT_DIR}/scripts/ops/maintenance.sh prune --quiet >> ${PROJECT_DIR}/logs/cron_maintenance.log 2>&1
EOF
		chmod 644 "$CRON_FILE"
		echo "✅ Cron schedule installed in ${CRON_FILE}:"
		echo "   - Daily DB backup at 03:00 UTC"
		echo "   - Weekly Docker & log cleanup on Sundays at 04:00 UTC"
		;;

	--help|-h|help|*)
		cat <<EOF
Usage: $0 <command>

Production AWS EC2 environment setup and hardening helper.

Commands:
  check | audit    Audit EC2 host readiness, Docker, Swap, .env permissions, and configs (Default)
  init-config      Scaffold .env (chmod 600), config/discord_ids.json, and filter directories
  swap             Create and persist a 2GB /swapfile to prevent OOM during Go/Docker builds (requires sudo)
  systemd          Install and enable syn-bot.service so containers start on EC2 reboot (requires sudo)
  cron             Install daily PostgreSQL backup & weekly Docker cleanup cron jobs (requires sudo)
  --help, -h       Show this help message
EOF
		exit 0
		;;
esac

