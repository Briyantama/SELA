#!/usr/bin/env bash
# Lints the protos and regenerates apps/api/gen/go with protoc-gen-go and protoc-gen-go-grpc (via buf).
#   scripts/proto-gen.sh           regenerate in place
#   scripts/proto-gen.sh --check   fail if the committed generated code is out of date (used by check.sh)
set -euo pipefail

cd "$(dirname "$0")/../apps/api"
export PATH="$(go env GOPATH)/bin:$PATH"

for tool in buf protoc-gen-go protoc-gen-go-grpc; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "missing tool: $tool (see buf.gen.yaml for install commands)" >&2
    exit 1
  fi
done

buf lint

if [ "${1:-}" = "--check" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  buf generate --output "$tmp"
  # contract_test.go is hand-written and lives next to the generated code; compare generated files only.
  if ! diff -r -x '*_test.go' "$tmp/gen/go" gen/go; then
    echo "generated code is out of date; run scripts/proto-gen.sh" >&2
    exit 1
  fi
  echo "proto: generated code is up to date"
else
  buf generate
  echo "proto: generated into apps/api/gen/go"
fi
