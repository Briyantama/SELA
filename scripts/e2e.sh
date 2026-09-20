#!/usr/bin/env bash
# Real-stack end-to-end run: starts the actual API against a throwaway Postgres database, the compose
# Redis and Mailpit, and lets Playwright drive Chrome through the Vite dev server (and its /api proxy).
#
#   bash scripts/e2e.sh [playwright args, e.g. host-happy-path]
#
# Needs: Node >= 20.19, Go, psql, curl, Docker with the compose services up:
#   docker compose --env-file .env -f deploy/docker-compose.yml up -d redis mailpit
# and TEST_DATABASE_URL pointing at any Postgres 14+ (used only to create/drop the throwaway database).
#
# Ports: the API runs on 8081/9091 and the web dev server on 5174, so a service already on the dev
# ports (8080/9090/5173) is never touched.
set -euo pipefail

cd "$(dirname "$0")/.."

fail() {
  echo "e2e: $*" >&2
  exit 1
}

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

: "${TEST_DATABASE_URL:?set TEST_DATABASE_URL, e.g. postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable (PGPASSWORD if needed)}"
[ -n "${REDIS_PASSWORD:-}" ] || fail "REDIS_PASSWORD is not set; copy .env.example to .env"
for tool in psql curl docker node go; do
  command -v "$tool" >/dev/null || fail "$tool not found on PATH"
done

listening() { (echo >"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

START_HINT="docker compose --env-file .env -f deploy/docker-compose.yml up -d redis mailpit"
listening 6379 || fail "Redis is not reachable on 6379. Start it with: $START_HINT"
listening 1025 || fail "Mailpit SMTP is not reachable on 1025. Start it with: $START_HINT"
listening 8025 || fail "Mailpit API is not reachable on 8025. Start it with: $START_HINT"

API_PORT=8081
GRPC_PORT_E2E=9091
WEB_PORT=5174
for port in "$API_PORT" "$GRPC_PORT_E2E" "$WEB_PORT"; do
  if listening "$port"; then fail "port $port is already in use; stop whatever holds it (the e2e run needs 8081, 9091 and 5174)"; fi
done

# A dev .env may leave the key empty; the API refuses to start without one.
OTP_HMAC_KEY="${OTP_HMAC_KEY:-$(openssl rand -hex 32)}"

WORK="$(mktemp -d)"
E2E_DB="sela_e2e_$$"
API_PID=""
# Same server, user and options as TEST_DATABASE_URL, with the database name swapped.
E2E_DB_URL="$(echo "$TEST_DATABASE_URL" | sed -E "s#(://[^/]+)/[^?]*#\1/$E2E_DB#")"

cleanup() {
  status=$?
  if [ -n "$API_PID" ]; then
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
  fi
  psql "$TEST_DATABASE_URL" -q -c "DROP DATABASE IF EXISTS $E2E_DB WITH (FORCE)" >/dev/null 2>&1 || true
  if [ "$status" -ne 0 ] && [ -f "$WORK/api.log" ]; then
    echo "--- last API log lines ---" >&2
    tail -n 15 "$WORK/api.log" >&2 || true
  fi
  rm -rf "$WORK"
  exit "$status"
}
trap cleanup EXIT

echo "==> e2e: throwaway database $E2E_DB, migrations, API build"
psql "$TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "CREATE DATABASE $E2E_DB"
(
  cd apps/api
  DATABASE_URL="$E2E_DB_URL" go run ./cmd/migrate up
  go build -o "$WORK/sela-api" ./cmd/api
)

echo "==> e2e: starting the API on :$API_PORT (gRPC :$GRPC_PORT_E2E)"
PORT="$API_PORT" GRPC_PORT="$GRPC_PORT_E2E" \
  DATABASE_URL="$E2E_DB_URL" \
  REDIS_ADDR="127.0.0.1:6379" REDIS_PASSWORD="$REDIS_PASSWORD" \
  SMTP_ADDR="127.0.0.1:1025" SMTP_FROM="no-reply@sela.local" \
  OTP_HMAC_KEY="$OTP_HMAC_KEY" \
  SHORT_LINK_BASE_URL="http://localhost:$WEB_PORT" \
  COOKIE_SECURE=false \
  "$WORK/sela-api" >"$WORK/api.log" 2>&1 &
API_PID=$!

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$API_PORT/healthz" >/dev/null 2>&1; then break; fi
  kill -0 "$API_PID" 2>/dev/null || fail "the API exited during start-up"
  sleep 0.5
done
curl -fsS "http://127.0.0.1:$API_PORT/healthz" >/dev/null || fail "the API did not become healthy within 30s"

echo "==> e2e: Playwright (Chrome, web on :$WEB_PORT)"
export E2E_API_URL="http://127.0.0.1:$API_PORT"
export E2E_WEB_URL="http://localhost:$WEB_PORT"
export E2E_MAILPIT_URL="http://127.0.0.1:8025"
export REDIS_PASSWORD
(cd apps/web && npx playwright test "$@")
