#!/usr/bin/env bash
# ==============================================================================
# open_db_for_dbeaver.sh
#
# Exposes the containerized PostgreSQL database to external clients (e.g. DBeaver)
# running on remote workstations.
#
# Supported Modes:
#   1. Instant Proxy (Default): Starts a lightweight TCP forwarder container
#      attached to the database network. Zero downtime; does NOT restart Postgres.
#   2. Compose Override (--compose): Generates docker-compose.override.yml with port mapping.
#   3. Stop (--stop): Stops and removes any active database exposure.
# ==============================================================================

set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
cd "$PROJECT_DIR"

CONTAINER_NAME="syn_db"
PROXY_NAME="syn_db_dbeaver_proxy"
OVERRIDE_FILE="docker-compose.override.yml"
DEFAULT_PORT="5432"

ACTION="start"
MODE="proxy"
HOST_PORT="$DEFAULT_PORT"
BIND_IP="0.0.0.0"

# Parse Command Line Options
while [[ $# -gt 0 ]]; do
    case "$1" in
        start)
            ACTION="start"
            shift
            ;;
        stop)
            ACTION="stop"
            shift
            ;;
        status)
            ACTION="status"
            shift
            ;;
        --stop)
            ACTION="stop"
            shift
            ;;
        --status)
            ACTION="status"
            shift
            ;;
        --ssh-only|-s)
            BIND_IP="127.0.0.1"
            shift
            ;;
        --port|-p)
            HOST_PORT="$2"
            shift 2
            ;;
        --compose|-c)
            MODE="compose"
            shift
            ;;
        --help|-h)
            echo "Usage: $0 [start|stop|status] [options]"
            echo ""
            echo "Commands:"
            echo "  start           Open PostgreSQL port for DBeaver (Default)"
            echo "  stop            Close database exposure and remove proxies"
            echo "  status          Check if the database port is currently exposed"
            echo ""
            echo "Options:"
            echo "  --ssh-only, -s  Bind to 127.0.0.1 only (Recommended for DBeaver SSH Tunnel)"
            echo "  --port, -p NUM  Specify host port to bind (Default: 5432)"
            echo "  --compose, -c   Use docker-compose.override.yml instead of zero-downtime proxy"
            echo "  --help, -h      Show this help dialog"
            exit 0
            ;;
        *)
            echo "Unknown argument: $1. Run with --help for usage." >&2
            exit 1
            ;;
    esac
done

# Load database credentials from .env if present
DB_USER="syn_admin"
DB_PASS=""
DB_NAME="syn_prod"

if [[ -f .env ]]; then
    # Safely extract variables without sourcing full file
    val=$(grep -E '^DB_USER=' .env | cut -d '=' -f2- | tr -d "\"' " || true)
    [[ -n "$val" ]] && DB_USER="$val"

    val=$(grep -E '^DB_PASSWORD=' .env | cut -d '=' -f2- | tr -d "\"' " || true)
    [[ -n "$val" ]] && DB_PASS="$val"

    val=$(grep -E '^DB_NAME=' .env | cut -d '=' -f2- | tr -d "\"' " || true)
    [[ -n "$val" ]] && DB_NAME="$val"
fi

# Detect Public IP
detect_public_ip() {
    local ip=""
    ip=$(curl -s --connect-timeout 2 http://169.254.169.254/latest/meta-data/public-ipv4 2>/dev/null || true)
    if [[ -z "$ip" ]]; then
        ip=$(curl -s --connect-timeout 2 https://ifconfig.me 2>/dev/null || true)
    fi
    if [[ -z "$ip" ]]; then
        ip=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
    fi
    echo "${ip:-<SERVER_IP>}"
}

print_dbeaver_instructions() {
    local host_addr
    local public_ip
    public_ip=$(detect_public_ip)

    if [[ "$BIND_IP" == "127.0.0.1" ]]; then
        host_addr="localhost (via SSH Tunnel)"
    else
        host_addr="$public_ip"
    fi

    echo ""
    echo "================================================================================"
    echo " 🐘 DBeaver PostgreSQL Connection Details"
    echo "================================================================================"
    echo " Host:           $host_addr"
    echo " Port:           $HOST_PORT"
    echo " Database:       $DB_NAME"
    echo " Username:       $DB_USER"
    if [[ -n "$DB_PASS" ]]; then
        echo " Password:       $DB_PASS"
    else
        echo " Password:       (Value of DB_PASSWORD in your .env)"
    fi
    echo " SSL Mode:       disable (or prefer)"
    echo "--------------------------------------------------------------------------------"

    if [[ "$BIND_IP" == "127.0.0.1" ]]; then
        echo " 🔒 SSH Tunnel Setup in DBeaver (Recommended for AWS EC2):"
        echo "   1. In DBeaver New Connection -> PostgreSQL -> Main tab:"
        echo "      - Host: localhost"
        echo "      - Port: $HOST_PORT"
        echo "      - Database: $DB_NAME"
        echo "      - Authentication: $DB_USER / password"
        echo "   2. Click the 'SSH' tab:"
        echo "      - Check 'Use SSH Tunnel'"
        echo "      - Host/IP: $public_ip"
        echo "      - Port: 22"
        echo "      - User name: ubuntu (or ec2-user)"
        echo "      - Authentication: Public Key (select your .pem private key file)"
        echo "   3. Click 'Test Connection'."
    else
        echo " 🌐 Direct Connection Setup in DBeaver:"
        echo "   1. In DBeaver New Connection -> PostgreSQL -> Main tab:"
        echo "      - Host: $public_ip"
        echo "      - Port: $HOST_PORT"
        echo "      - Database: $DB_NAME"
        echo "      - Authentication: $DB_USER / password"
        echo "   2. Ensure AWS EC2 Security Group allows Inbound TCP port $HOST_PORT from your IP."
    fi
    echo "================================================================================"
    echo " When finished, close the database exposure by running:"
    echo "   $0 stop"
    echo "================================================================================"
}

# ------------------------------------------------------------------------------
# Action: STATUS
# ------------------------------------------------------------------------------
if [[ "$ACTION" == "status" ]]; then
    found=0
    if docker ps --format '{{.Names}}' | grep -q "^${PROXY_NAME}$"; then
        echo "🟢 Instant Proxy is ACTIVE: Container '$PROXY_NAME' is forwarding port $HOST_PORT to Postgres."
        found=1
    fi

    if [[ -f "$OVERRIDE_FILE" ]]; then
        echo "🟢 Compose Override is ACTIVE: '$OVERRIDE_FILE' exists."
        found=1
    fi

    if [[ $found -eq 0 ]]; then
        echo "⚪ Database port is NOT currently exposed."
    fi
    exit 0
fi

# ------------------------------------------------------------------------------
# Action: STOP
# ------------------------------------------------------------------------------
if [[ "$ACTION" == "stop" ]]; then
    stopped=0
    if docker ps -a --format '{{.Names}}' | grep -q "^${PROXY_NAME}$"; then
        echo "Stopping and removing proxy container '$PROXY_NAME'..."
        docker stop "$PROXY_NAME" >/dev/null 2>&1 || true
        docker rm "$PROXY_NAME" >/dev/null 2>&1 || true
        stopped=1
    fi

    if [[ -f "$OVERRIDE_FILE" ]]; then
        echo "Removing '$OVERRIDE_FILE'..."
        rm -f "$OVERRIDE_FILE"
        echo "Restarting postgres container to revoke port exposure..."
        docker compose up -d postgres --no-build
        stopped=1
    fi

    if [[ $stopped -eq 1 ]]; then
        echo "✅ Database external exposure has been successfully closed."
    else
        echo "ℹ️  No active database exposure was running."
    fi
    exit 0
fi

# ------------------------------------------------------------------------------
# Action: START
# ------------------------------------------------------------------------------

# Verify postgres container is running
if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
    echo "⚠️  Postgres container '$CONTAINER_NAME' is not currently running."
    echo "Starting postgres container with docker compose..."
    docker compose up -d postgres
    sleep 2
fi

if [[ "$MODE" == "compose" ]]; then
    echo "Creating '$OVERRIDE_FILE' with port mapping ${BIND_IP}:${HOST_PORT}:5432..."
    cat > "$OVERRIDE_FILE" <<EOF
services:
  postgres:
    ports:
      - "${BIND_IP}:${HOST_PORT}:5432"
EOF
    echo "Applying Compose override..."
    docker compose up -d postgres --no-build
    echo "✅ Postgres port $HOST_PORT is now exposed via Docker Compose."
    print_dbeaver_instructions
    exit 0
fi

# Default Mode: Instant TCP Proxy (Zero Downtime)
# Find the network syn_db is attached to
DB_NETWORK=$(docker inspect "$CONTAINER_NAME" --format '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' 2>/dev/null || true)
if [[ -z "$DB_NETWORK" ]]; then
    echo "Error: Could not determine Docker network for container '$CONTAINER_NAME'." >&2
    exit 1
fi

# Remove previous proxy container if exists
if docker ps -a --format '{{.Names}}' | grep -q "^${PROXY_NAME}$"; then
    docker rm -f "$PROXY_NAME" >/dev/null 2>&1 || true
fi

echo "Starting zero-downtime TCP proxy on ${BIND_IP}:${HOST_PORT} -> ${CONTAINER_NAME}:5432..."
docker run -d \
    --name "$PROXY_NAME" \
    --network "$DB_NETWORK" \
    --restart unless-stopped \
    -p "${BIND_IP}:${HOST_PORT}:5432" \
    alpine:latest \
    sh -c "apk add --no-cache socat >/dev/null 2>&1 && exec socat TCP-LISTEN:5432,fork,reuseaddr TCP:${CONTAINER_NAME}:5432" >/dev/null

echo "✅ Database proxy running successfully."
print_dbeaver_instructions
