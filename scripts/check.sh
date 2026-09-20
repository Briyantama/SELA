#!/usr/bin/env bash
# Runs the full local validation: Go (format, vet, lint, tests + coverage), web (type check, tests + coverage)
# and the real-stack end-to-end run (scripts/e2e.sh; set SKIP_E2E=1 to skip it explicitly).
# The web app needs Node >= 20.19 (Vite 8). Run from the repo root.
set -euo pipefail

cd "$(dirname "$0")/.."

# DB tests skip themselves when this is unset; the full check must never skip them silently.
# Point it at any PostgreSQL 14+ server (e.g. deploy/docker-compose.yml); each test creates and drops its own database.
if [ -z "${TEST_DATABASE_URL:-}" ]; then
  echo "TEST_DATABASE_URL is not set, e.g. postgres://sela:<password>@127.0.0.1:5432/postgres?sslmode=disable" >&2
  exit 1
fi

echo "==> Proto: lint and generated-code drift"
bash scripts/proto-gen.sh --check

echo "==> Go (apps/api): format check"
unformatted="$(cd apps/api && gofmt -l cmd internal services gen 2>/dev/null || true)"
if [ -n "$unformatted" ]; then
  echo "gofmt needed on:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "==> Go (apps/api): vet, lint, test (unit, gRPC handlers, migrations)"
(
  cd apps/api
  go vet ./...
  golangci-lint run --timeout 5m
  go test ./... -cover
)

echo "==> Web: check, test"
(
  cd apps/web
  npm run check
  npm run test:coverage
)

# Real-stack end-to-end run (live OTP sign-in and the host happy path). It needs Docker with Redis and
# Mailpit up (see scripts/e2e.sh) and the installed Google Chrome. It fails loudly when those are
# missing; skipping is an explicit choice that is announced here and again in the final line.
if [ "${SKIP_E2E:-}" = "1" ]; then
  echo "==> E2E: SKIPPED (SKIP_E2E=1)" >&2
  echo "!!! The live OTP flow and the host happy path were NOT verified by this run." >&2
  echo "All checks passed (E2E skipped)."
else
  echo "==> E2E: real API, Redis, Mailpit and Playwright"
  bash scripts/e2e.sh
  echo "All checks passed."
fi
