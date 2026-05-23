#!/bin/sh
# init-env.sh — runs inside an alpine container; generates .env from .env.template.
# All secrets here are local to this machine and never leave it.
set -eu

cd /work

if [ -f .env ]; then
  echo "[init] .env already exists — skipping"
  exit 0
fi

if [ ! -f .env.template ]; then
  echo "[init][error] .env.template not found in /work" >&2
  exit 1
fi

# alpine base image lacks openssl — install it.
apk add --no-cache openssl >/dev/null 2>&1

DB_PW=$(openssl rand -base64 24 | tr -d '/=+' | cut -c1-32)

# Generate RSA 2048 PKCS1 PEM, then collapse newlines into literal \n
# so it fits on a single .env line (the Go side un-escapes \n back).
JWT_PEM=$(openssl genrsa 2048 2>/dev/null)
JWT_ESC=$(printf '%s' "$JWT_PEM" | sed ':a;N;$!ba;s/\n/\\n/g')

# Use | as sed delimiter — neither value contains it.
sed \
  -e "s|__DB_PASSWORD__|${DB_PW}|" \
  -e "s|__JWT_PRIVATE_KEY__|${JWT_ESC}|" \
  .env.template > .env

chmod 600 .env

# On Mac/Linux the start script passes the host UID/GID so the file ends up
# owned by the user, not root. On Windows Docker Desktop maps ownership
# transparently and HOST_UID is unset.
if [ -n "${HOST_UID:-}" ] && [ -n "${HOST_GID:-}" ]; then
  chown "${HOST_UID}:${HOST_GID}" .env
fi

echo "[init] .env generated."
