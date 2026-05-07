#!/usr/bin/env bash
# deploy.sh — Stone Server production deployment on AWS Lightsail (Ubuntu 22.04)
#
# Usage:
#   1. SSH into the Lightsail instance
#   2. Clone the repo and cd into it
#   3. Copy .env.example to .env and fill in all values
#   4. Run: bash deploy.sh <your.domain.com> <email@example.com>
#
# What this script does:
#   - Installs Docker + Docker Compose plugin if missing
#   - Starts services with self-signed cert so nginx is reachable on port 80
#   - Issues a Let's Encrypt certificate via the ACME HTTP-01 webroot challenge
#   - Updates .env to point nginx at the LE cert
#   - Reloads nginx with the real cert
#   - Starts the certbot renewal daemon (--profile production)
#   - Confirms HTTPS health check
set -euo pipefail

DOMAIN="${1:?Usage: $0 <domain> <email>}"
EMAIL="${2:?Usage: $0 <domain> <email>}"

# ── 1. Install Docker if missing ─────────────────────────────────────────────
if ! command -v docker &>/dev/null; then
  echo "==> Installing Docker..."
  curl -fsSL https://get.docker.com | sh
  sudo usermod -aG docker "$USER"
  echo "Docker installed. Re-login or run 'newgrp docker' if permission errors occur."
fi

if ! docker compose version &>/dev/null; then
  echo "==> Installing Docker Compose plugin..."
  sudo apt-get install -y docker-compose-plugin
fi

# ── 2. Verify .env exists and is not tracked by git ─────────────────────────
if [[ ! -f .env ]]; then
  echo "ERROR: .env not found. Copy .env.example to .env and fill in values."
  exit 1
fi
if git ls-files --error-unmatch .env &>/dev/null 2>&1; then
  echo "ERROR: .env is tracked by git. Remove it with: git rm --cached .env"
  exit 1
fi

# ── 3. Start services with self-signed cert (nginx reachable on port 80) ─────
echo "==> Starting services (self-signed cert)..."
docker compose up -d --build

echo "==> Waiting for nginx to be ready..."
for i in $(seq 1 30); do
  if curl -sf http://localhost/api/v1/health &>/dev/null; then break; fi
  sleep 2
done

# ── 4. Issue Let's Encrypt certificate (webroot challenge) ───────────────────
echo "==> Issuing Let's Encrypt certificate for ${DOMAIN}..."
docker compose run --rm --entrypoint certbot certbot certonly \
  --webroot \
  --webroot-path /var/www/certbot \
  --email "${EMAIL}" \
  --agree-tos \
  --no-eff-email \
  -d "${DOMAIN}"

# ── 5. Switch .env to LE cert paths and reload nginx ────────────────────────
echo "==> Switching nginx to Let's Encrypt certificate..."
sed -i "s|^DOMAIN=.*|DOMAIN=${DOMAIN}|" .env
sed -i "s|^SSL_CERT=.*|SSL_CERT=/etc/letsencrypt/live/${DOMAIN}/fullchain.pem|" .env
sed -i "s|^SSL_KEY=.*|SSL_KEY=/etc/letsencrypt/live/${DOMAIN}/privkey.pem|" .env
# If lines don't exist yet, append them
grep -q "^SSL_CERT=" .env || echo "SSL_CERT=/etc/letsencrypt/live/${DOMAIN}/fullchain.pem" >> .env
grep -q "^SSL_KEY="  .env || echo "SSL_KEY=/etc/letsencrypt/live/${DOMAIN}/privkey.pem"  >> .env

docker compose up -d nginx  # re-creates nginx with updated env

echo "==> Waiting for nginx to reload..."
sleep 5

# ── 6. Start certbot renewal daemon ─────────────────────────────────────────
echo "==> Starting certbot renewal daemon..."
docker compose --profile production up -d certbot

# ── 7. Firewall check reminder ───────────────────────────────────────────────
echo ""
echo "==> Firewall reminder:"
echo "    In the Lightsail console → Networking → Firewall, ensure:"
echo "      - TCP 80  (HTTP)  — open"
echo "      - TCP 443 (HTTPS) — open"
echo "      - TCP 5432 (PostgreSQL) — CLOSED"
echo "      - TCP 6379 (Redis)      — CLOSED"

# ── 8. Verify HTTPS health check ────────────────────────────────────────────
echo ""
echo "==> Verifying HTTPS health check..."
if curl -sf "https://${DOMAIN}/api/v1/health" | grep -q '"status"'; then
  echo "    OK: https://${DOMAIN}/api/v1/health is responding."
else
  echo "    WARNING: health check did not return expected response."
  echo "    Check: docker compose logs nginx app"
fi

# ── 9. Verify HTTP → HTTPS redirect ─────────────────────────────────────────
REDIRECT=$(curl -sI "http://${DOMAIN}/api/v1/health" | grep -i "^location:")
if echo "${REDIRECT}" | grep -q "https://"; then
  echo "    OK: HTTP → HTTPS redirect confirmed."
else
  echo "    WARNING: HTTP redirect not detected. Check nginx config."
fi

echo ""
echo "==> Deployment complete."
echo "    Cert expires in 90 days. Auto-renewal runs every 12h via the certbot container."
echo "    Check renewal: docker compose --profile production logs certbot"
