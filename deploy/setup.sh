#!/usr/bin/env bash
# Build and install the gmem MCP server as an HTTPS service on a fresh Ubuntu VPS.
#
# Run as root on the VPS, from a directory containing the gmem source:
#   sudo GMEM_DOMAIN=gmem.example.com ./deploy/setup.sh
#
# Idempotent: re-running rebuilds the binary and restarts the service without
# regenerating secrets or the deploy key.
set -euo pipefail

DOMAIN="${GMEM_DOMAIN:?set GMEM_DOMAIN, e.g. GMEM_DOMAIN=gmem.example.com}"
MEMORY_REMOTE="${GMEM_MEMORY_REMOTE:?set GMEM_MEMORY_REMOTE to the git remote holding your memories}"
GIT_USER_NAME="${GMEM_GIT_NAME:-gmem}"
GIT_USER_EMAIL="${GMEM_GIT_EMAIL:-gmem@${DOMAIN}}"
GO_VERSION="${GMEM_GO_VERSION:-1.26.3}"

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_USER=gmem
SERVICE_HOME=/var/lib/gmem
ENV_FILE=/etc/gmem/env
BIN=/usr/local/bin/git-mcp-memory

[[ $EUID -eq 0 ]] || { echo "run as root (sudo)"; exit 1; }

say() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

say "パッケージを導入"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq git curl ca-certificates build-essential

say "Go ${GO_VERSION} を確認"
# The module requires a recent toolchain; distro packages lag well behind it.
need_go=1
if command -v go >/dev/null 2>&1; then
    current="$(go version | awk '{print $3}' | sed 's/^go//')"
    [[ "$current" == "$GO_VERSION" ]] && need_go=0
fi
if [[ $need_go -eq 1 ]]; then
    case "$(uname -m)" in
        x86_64)  goarch=amd64 ;;
        aarch64) goarch=arm64 ;;
        *) echo "unsupported arch: $(uname -m)"; exit 1 ;;
    esac
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${goarch}.tar.gz" -o /tmp/go.tgz
    rm -rf /usr/local/go
    tar -C /usr/local -xzf /tmp/go.tgz
    rm -f /tmp/go.tgz
fi
export PATH=/usr/local/go/bin:$PATH

say "サービスユーザー ${SERVICE_USER} を用意"
id -u "$SERVICE_USER" >/dev/null 2>&1 || useradd --system --create-home --home-dir "$SERVICE_HOME" --shell /usr/sbin/nologin "$SERVICE_USER"
install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 700 "$SERVICE_HOME/.ssh"
install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 755 "$SERVICE_HOME/.config/git-mcp-memory" "$SERVICE_HOME/.local/share/git-mcp-memory"

say "ビルド"
# cgo is required by onnxruntime_go, so the binary is built here rather than
# cross-compiled from a workstation.
(cd "$SRC_DIR" && CGO_ENABLED=1 go build -o "$BIN" ./cmd/git-mcp-memory)
chmod 755 "$BIN"
"$BIN" schema >/dev/null && echo "binary OK: $($BIN schema | head -c 40)..."

say "メモリリポジトリ用のデプロイキー"
DEPLOY_KEY="$SERVICE_HOME/.ssh/id_ed25519"
if [[ ! -f "$DEPLOY_KEY" ]]; then
    sudo -u "$SERVICE_USER" ssh-keygen -t ed25519 -N '' -C "gmem@${DOMAIN}" -f "$DEPLOY_KEY" >/dev/null
    NEW_KEY=1
fi
sudo -u "$SERVICE_USER" ssh-keyscan -t rsa,ed25519 github.com 2>/dev/null >> "$SERVICE_HOME/.ssh/known_hosts"
sort -u -o "$SERVICE_HOME/.ssh/known_hosts" "$SERVICE_HOME/.ssh/known_hosts"
chown "$SERVICE_USER:$SERVICE_USER" "$SERVICE_HOME/.ssh/known_hosts"

say "設定ファイル"
# Only remote_url is required; every other field falls back to a Linux default.
sudo -u "$SERVICE_USER" tee "$SERVICE_HOME/.config/git-mcp-memory/config.json" >/dev/null <<JSON
{
  "remote_url": "${MEMORY_REMOTE}"
}
JSON
sudo -u "$SERVICE_USER" git config --global user.name "$GIT_USER_NAME"
sudo -u "$SERVICE_USER" git config --global user.email "$GIT_USER_EMAIL"
sudo -u "$SERVICE_USER" git config --global --add safe.directory '*'

say "認証情報"
install -d -m 750 /etc/gmem
if [[ ! -f "$ENV_FILE" ]]; then
    cat > "$ENV_FILE" <<ENV
# Static bearer token for clients that can set a header (Claude Code --header).
GMEM_HTTP_TOKEN=$(openssl rand -hex 32)
# Password for the OAuth consent screen, for clients that cannot (claude.ai).
GMEM_OAUTH_PASSWORD=$(openssl rand -base64 18 | tr -d '/+=' | head -c 20)
GMEM_PUBLIC_URL=https://${DOMAIN}
GMEM_HTTP_ADDR=127.0.0.1:8765
ENV
    chmod 640 "$ENV_FILE"
fi

say "systemd"
install -m 644 "$SRC_DIR/deploy/gmem-http.service" /etc/systemd/system/gmem-http.service
systemctl daemon-reload
systemctl enable --now gmem-http

say "nginx + TLS 証明書"
# The host already serves other vhosts through nginx, so this adds a vhost
# rather than introducing a second web server on ports 80/443.
command -v nginx >/dev/null 2>&1 || apt-get install -y -qq nginx
command -v certbot >/dev/null 2>&1 || apt-get install -y -qq certbot

SITE_AVAILABLE="/etc/nginx/sites-available/${DOMAIN}"
if [[ ! -f "/etc/letsencrypt/live/${DOMAIN}/fullchain.pem" ]]; then
    # The vhost cannot reference a certificate that does not exist yet, so the
    # ACME challenge is served from a port-80-only vhost first.
    install -d /var/www/html
    cat > "$SITE_AVAILABLE" <<CONF
server {
    listen 80;
    listen [::]:80;
    server_name ${DOMAIN};
    location ^~ /.well-known/acme-challenge/ { root /var/www/html; }
    location / { return 404; }
}
CONF
    ln -sf "$SITE_AVAILABLE" "/etc/nginx/sites-enabled/${DOMAIN}"
    nginx -t && systemctl reload nginx
    certbot certonly --webroot -w /var/www/html -d "${DOMAIN}" \
        --non-interactive --agree-tos --register-unsafely-without-email
fi

sed "s|{{DOMAIN}}|${DOMAIN}|g" "$SRC_DIR/deploy/nginx-gmem.conf" > "$SITE_AVAILABLE"
ln -sf "$SITE_AVAILABLE" "/etc/nginx/sites-enabled/${DOMAIN}"
nginx -t && systemctl reload nginx

say "完了"
systemctl --no-pager --lines=5 status gmem-http || true
echo
echo "接続情報:"
grep -E '^GMEM_(HTTP_TOKEN|OAUTH_PASSWORD)=' "$ENV_FILE" | sed 's/^/  /'
echo "  MCP URL: https://${DOMAIN}/mcp"
if [[ "${NEW_KEY:-0}" == "1" ]]; then
    echo
    echo "!! この公開鍵を ${MEMORY_REMOTE} の Deploy keys に *書き込み許可つき* で登録してください:"
    echo
    cat "${DEPLOY_KEY}.pub"
    echo
    echo "   登録後: sudo -u ${SERVICE_USER} HOME=${SERVICE_HOME} ${BIN} sync"
fi
