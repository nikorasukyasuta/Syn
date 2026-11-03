#!/usr/bin/env bash
# ==============================================================================
# scripts/ops/setup_domain_ssl.sh
#
# Configures an AWS EC2 instance (with an AWS Elastic IP) and a Namecheap domain
# to publicly serve the Syn Website Portal (/), Terms & Conditions (/terms),
# and Privacy Policy (/privacy) over HTTPS (ports 80 & 443 -> 127.0.0.1:8080)
# and updates WEB_BASE_URL so the Discord bot's /terms and /privacy commands
# link directly to the live domain.
# ==============================================================================

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
cd "$PROJECT_DIR"

ACTION="${1:-help}"
if [[ $# -gt 0 ]]; then
	shift
fi

DOMAIN=""
EMAIL=""
HTTP_ONLY=0
INCLUDE_WWW=0

while [[ $# -gt 0 ]]; do
	case "$1" in
		--domain|-d)
			DOMAIN="${2:-}"
			shift 2
			;;
		--email|-e)
			EMAIL="${2:-}"
			shift 2
			;;
		--http-only)
			HTTP_ONLY=1
			shift
			;;
		--www)
			INCLUDE_WWW=1
			shift
			;;
		--help|-h)
			ACTION="help"
			shift
			;;
		*)
			if [[ -z "$DOMAIN" && "$1" != -* ]]; then
				DOMAIN="$1"
				shift
			else
				echo "Error: Unknown argument '$1'." >&2
				exit 1
			fi
			;;
	esac
done

# Strip any leading http:// or https:// and trailing slashes from DOMAIN
DOMAIN="$(echo "$DOMAIN" | sed -E 's#^https?://##; s#/.*$##' | tr '[:upper:]' '[:lower:]')"

get_web_port() {
	local port="8080"
	if [[ -f "${PROJECT_DIR}/.env" ]]; then
		local env_port
		env_port=$(grep -E '^WEB_PORT=' "${PROJECT_DIR}/.env" | tail -n 1 | cut -d '=' -f2- | tr -d "\"' " || true)
		[[ -n "$env_port" ]] && port="$env_port"
	fi
	echo "$port"
}

get_public_ip() {
	local ip=""
	# Try AWS EC2 IMDSv2 first
	local token
	token=$(curl -fsS --max-time 2 -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 60" 2>/dev/null || true)
	if [[ -n "$token" ]]; then
		ip=$(curl -fsS --max-time 2 -H "X-aws-ec2-metadata-token: $token" "http://169.254.169.254/latest/meta-data/public-ipv4" 2>/dev/null || true)
	fi
	if [[ -z "$ip" ]]; then
		ip=$(curl -fsS --max-time 3 "https://checkip.amazonaws.com" 2>/dev/null | tr -d '[:space:]' || true)
	fi
	echo "${ip:-<YOUR_AWS_ELASTIC_IP>}"
}

resolve_domain_ip() {
	local target="$1"
	local resolved=""
	if command -v getent >/dev/null 2>&1; then
		resolved=$(getent ahosts "$target" 2>/dev/null | awk '/STREAM/ {print $1; exit}' || true)
	fi
	if [[ -z "$resolved" ]] && command -v dig >/dev/null 2>&1; then
		resolved=$(dig +short A "$target" 2>/dev/null | grep -E '^[0-9]+\.' | head -n 1 || true)
	fi
	echo "$resolved"
}

print_namecheap_guide() {
	local target_domain="${1:-yourdomain.com}"
	local elastic_ip
	elastic_ip="$(get_public_ip)"

	cat <<EOF
================================================================================
 🌐 Namecheap DNS + AWS Elastic IP Configuration Guide
================================================================================
1. AWS EC2 Security Group (Inbound Rules):
   Ensure your EC2 instance's Security Group allows inbound traffic on:
   • Port 80  (TCP) — Source: 0.0.0.0/0 (HTTP & Let's Encrypt ACME verification)
   • Port 443 (TCP) — Source: 0.0.0.0/0 (HTTPS web traffic)

2. Namecheap Domain Setup (Domain List -> Manage -> Advanced DNS):
   Remove any default Namecheap Parking records, and add these Host Records:
   ┌──────────────┬───────────┬──────────────────────────┬───────────┐
   │ Type         │ Host      │ Value                    │ TTL       │
   ├──────────────┼───────────┼──────────────────────────┼───────────┤
   │ A Record     │ @         │ ${elastic_ip}            │ Automatic │
   │ CNAME Record │ www       │ ${target_domain}.        │ Automatic │
   └──────────────┴───────────┴──────────────────────────┴───────────┘

3. Provision Nginx Reverse Proxy & HTTPS on EC2:
   Once DNS resolves to ${elastic_ip}, run:
     sudo ./scripts/synctl.sh domain setup ${target_domain} --email admin@${target_domain}

4. Verify Pages & Discord Bot Commands:
   • Website Portal:       https://${target_domain}/
   • Terms & Conditions:   https://${target_domain}/terms
   • Privacy Policy:       https://${target_domain}/privacy
   • Discord Commands:     Run /terms and /privacy inside your Discord server
================================================================================
EOF
}

check_domain_status() {
	local web_port
	web_port="$(get_web_port)"
	local elastic_ip
	elastic_ip="$(get_public_ip)"

	echo "================================================================================"
	echo " 🔍 Syn Website, Terms & Privacy Domain Diagnostics"
	echo "================================================================================"
	echo "  • EC2 Public / Elastic IP:  ${elastic_ip}"
	echo "  • Local Bot Web Port:       ${web_port}"

	if [[ -f "${PROJECT_DIR}/.env" ]]; then
		local current_base
		current_base=$(grep -E '^WEB_BASE_URL=' "${PROJECT_DIR}/.env" | tail -n 1 | cut -d '=' -f2- | tr -d "\"' " || true)
		echo "  • Configured WEB_BASE_URL:  ${current_base:-<not set in .env>}"
	fi

	echo ""
	echo "--- 1. Local Bot Web Server Endpoints (127.0.0.1:${web_port}) ---"
	for endpoint in "/" "/terms" "/privacy" "/health"; do
		if status_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 2 "http://127.0.0.1:${web_port}${endpoint}" 2>/dev/null); then
			if [[ "$status_code" == "200" ]]; then
				echo "  ✅ GET http://127.0.0.1:${web_port}${endpoint} -> HTTP ${status_code}"
			else
				echo "  ⚠️  GET http://127.0.0.1:${web_port}${endpoint} -> HTTP ${status_code}"
			fi
		else
			echo "  ❌ GET http://127.0.0.1:${web_port}${endpoint} -> Connection failed (is syn_app running?)"
		fi
	done

	if [[ -n "$DOMAIN" ]]; then
		echo ""
		echo "--- 2. DNS Resolution for '${DOMAIN}' ---"
		local resolved_ip
		resolved_ip="$(resolve_domain_ip "$DOMAIN")"
		if [[ -z "$resolved_ip" ]]; then
			echo "  ❌ '${DOMAIN}' did not resolve to an IPv4 A record yet."
			echo "     Add an A Record (@ -> ${elastic_ip}) in Namecheap -> Advanced DNS."
		elif [[ "$resolved_ip" == "$elastic_ip" ]]; then
			echo "  ✅ '${DOMAIN}' resolves to ${resolved_ip} (matches EC2 Elastic IP!)"
		else
			echo "  ⚠️  '${DOMAIN}' resolves to ${resolved_ip} (EC2 Elastic IP is ${elastic_ip})"
		fi
	fi
	echo "================================================================================"
}

update_env_web_base_url() {
	local new_url="$1"
	local env_file="${PROJECT_DIR}/.env"

	if [[ ! -f "$env_file" ]]; then
		echo "WEB_BASE_URL=${new_url}" > "$env_file"
		chmod 600 "$env_file"
		echo "✅ Created ${env_file} with WEB_BASE_URL=${new_url}"
		return
	fi

	if grep -qE '^WEB_BASE_URL=' "$env_file"; then
		sed -i.bak -E "s|^WEB_BASE_URL=.*|WEB_BASE_URL=${new_url}|" "$env_file"
		rm -f "${env_file}.bak"
	else
		printf "\n# Public Host URL for Web Terms & Privacy Links\nWEB_BASE_URL=%s\n" "$new_url" >> "$env_file"
	fi
	echo "✅ Updated WEB_BASE_URL=${new_url} in ${env_file}"
}

setup_nginx_and_ssl() {
	if [[ -z "$DOMAIN" ]]; then
		echo "Error: Domain name is required. Usage: sudo $0 setup <yourdomain.com> [--email you@example.com]" >&2
		exit 1
	fi

	if [[ $EUID -ne 0 ]]; then
		echo "Error: Setting up Nginx and SSL certificates requires root privileges. Run with sudo:" >&2
		echo "  sudo $0 setup ${DOMAIN} ${EMAIL:+--email $EMAIL}" >&2
		exit 1
	fi

	local web_port
	web_port="$(get_web_port)"

	echo "================================================================================"
	echo " 🚀 Configuring Nginx Reverse Proxy & HTTPS for ${DOMAIN}"
	echo "================================================================================"

	# 1. Install Nginx & Certbot if missing
	if ! command -v nginx >/dev/null 2>&1; then
		echo "📦 Installing Nginx..."
		if command -v apt-get >/dev/null 2>&1; then
			apt-get update -y
			apt-get install -y nginx
		elif command -v dnf >/dev/null 2>&1; then
			dnf install -y nginx
		elif command -v yum >/dev/null 2>&1; then
			yum install -y nginx
		else
			echo "Error: Unsupported package manager. Please install nginx manually." >&2
			exit 1
		fi
	fi

	if [[ $HTTP_ONLY -eq 0 ]] && ! command -v certbot >/dev/null 2>&1; then
		echo "📦 Installing Certbot for Let's Encrypt HTTPS..."
		if command -v apt-get >/dev/null 2>&1; then
			apt-get update -y
			apt-get install -y certbot python3-certbot-nginx
		elif command -v dnf >/dev/null 2>&1; then
			dnf install -y certbot python3-certbot-nginx
		elif command -v yum >/dev/null 2>&1; then
			yum install -y certbot python3-certbot-nginx
		fi
	fi

	local server_names="${DOMAIN}"
	if [[ $INCLUDE_WWW -eq 1 ]]; then
		server_names="${DOMAIN} www.${DOMAIN}"
	fi

	# 2. Write Nginx reverse proxy configuration
	local nginx_conf="/etc/nginx/conf.d/syn_portal.conf"
	if [[ -d /etc/nginx/sites-available && -d /etc/nginx/sites-enabled ]]; then
		nginx_conf="/etc/nginx/sites-available/syn_portal.conf"
	fi

	cat > "$nginx_conf" <<EOF
server {
    listen 80;
    listen [::]:80;
    server_name ${server_names};

    # Restrict internal resource telemetry endpoint to localhost only
    location = /metrics {
        allow 127.0.0.1;
        allow ::1;
        deny all;
        proxy_pass http://127.0.0.1:${web_port}/metrics;
    }

    # Public Website (/), Terms (/terms), Privacy (/privacy), and Health (/health)
    location / {
        proxy_pass http://127.0.0.1:${web_port};
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_read_timeout 15s;
        proxy_connect_timeout 5s;
    }
}
EOF

	if [[ -d /etc/nginx/sites-enabled && "$nginx_conf" == "/etc/nginx/sites-available/syn_portal.conf" ]]; then
		ln -sf "$nginx_conf" /etc/nginx/sites-enabled/syn_portal.conf
		rm -f /etc/nginx/sites-enabled/default
	fi

	nginx -t
	systemctl enable nginx
	systemctl reload nginx || systemctl restart nginx
	echo "✅ Nginx reverse proxy active on port 80 -> 127.0.0.1:${web_port}"

	local final_scheme="http"
	if [[ $HTTP_ONLY -eq 0 ]]; then
		echo "🔒 Requesting Let's Encrypt TLS certificate for ${DOMAIN}..."
		local certbot_args=(--nginx -d "$DOMAIN" --non-interactive --agree-tos --redirect)
		if [[ $INCLUDE_WWW -eq 1 ]]; then
			certbot_args+=(-d "www.${DOMAIN}")
		fi
		if [[ -n "$EMAIL" ]]; then
			certbot_args+=(--email "$EMAIL")
		else
			certbot_args+=(--register-unsafely-without-email)
		fi

		if certbot "${certbot_args[@]}"; then
			final_scheme="https"
			echo "✅ HTTPS enabled successfully for https://${DOMAIN}"
		else
			echo "⚠️  Certbot TLS provisioning did not complete (verify Namecheap DNS A record points to this EC2 Elastic IP and port 80/443 are open in the AWS Security Group)."
			echo "   Serving over HTTP (http://${DOMAIN}) until Certbot is re-run."
		fi
	fi

	local public_base_url="${final_scheme}://${DOMAIN}"
	update_env_web_base_url "$public_base_url"

	# Restart bot container if running so it picks up the new WEB_BASE_URL immediately
	if command -v docker >/dev/null 2>&1 && docker ps --format '{{.Names}}' | grep -q '^syn_app$'; then
		echo "🔄 Recreating syn_app container to apply WEB_BASE_URL=${public_base_url}..."
		docker compose up --detach --no-build --no-deps bot
	fi

	echo "================================================================================"
	echo "✅ Domain & Web Portal Setup Complete!"
	echo "   • Website Portal:     ${public_base_url}/"
	echo "   • Terms & Conditions: ${public_base_url}/terms"
	echo "   • Privacy Policy:     ${public_base_url}/privacy"
	echo "   • Discord Bot Links:  /terms and /privacy now point to ${public_base_url}"
	echo "================================================================================"
}

case "$ACTION" in
	guide|dns)
		print_namecheap_guide "${DOMAIN:-yourdomain.com}"
		;;
	check|status|verify)
		check_domain_status
		;;
	setup|install)
		setup_nginx_and_ssl
		;;
	set-url)
		if [[ -z "$DOMAIN" ]]; then
			echo "Error: Provide a domain or URL, e.g.: $0 set-url https://yourdomain.com" >&2
			exit 1
		fi
		update_env_web_base_url "https://${DOMAIN}"
		;;
	help|--help|-h|*)
		cat <<EOF
Usage: $0 <command> [domain] [options]

Commands:
  guide [domain]                            Show Namecheap Advanced DNS + AWS Elastic IP guide
  check [domain]                            Verify local web endpoints and DNS A-record resolution
  setup <domain> [--email <email>] [--www]  Configure Nginx reverse proxy (80/443 -> 8080), Let's Encrypt SSL, and update WEB_BASE_URL
  set-url <domain>                          Update WEB_BASE_URL=https://<domain> in .env

Options:
  --email, -e <email>   Email for Let's Encrypt SSL expiration notices
  --www                 Also include www.<domain> in Nginx & SSL certificate
  --http-only           Configure Nginx on port 80 only (skip Let's Encrypt HTTPS)
EOF
		exit 0
		;;
esac

