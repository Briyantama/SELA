#!/usr/bin/env bash
# Runs the full local validation: Go (format, vet, lint, tests + coverage) and web (type check, tests + coverage).
# The web app needs Node >= 20.19 (Vite 8). Run from the repo root.
set -euo pipefail

cd "$(dirname "$0")/.."

echo "==> Go: format check"
unformatted="$(gofmt -l cmd internal services 2>/dev/null || true)"
if [ -n "$unformatted" ]; then
  echo "gofmt needed on:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "==> Go: vet, lint, test"
go vet ./...
golangci-lint run
go test ./... -cover

echo "==> Web: check, test"
(
  cd apps/web
  npm run check
  npm run test:coverage
)

echo "All checks passed."
