#!/usr/bin/env bash
# Guided end-to-end demo. Checks prerequisites, then runs the durability proof:
# a canary deploy is interrupted by killing the worker mid-bake and resumes on
# restart with no duplicate side effects.
#
# Prerequisites (start these first, in separate terminals):
#   make cluster && make kyverno     # kind cluster + image policy
#   make server                      # Temporal dev server (UI on :8233)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

echo "checking prerequisites..."

if ! temporal operator namespace list >/dev/null 2>&1; then
  echo "  Temporal dev server not reachable on 127.0.0.1:7233 — run 'make server'." >&2
  exit 1
fi
echo "  Temporal dev server: up"

if ! kubectl -n temporalops get deployment web-stable >/dev/null 2>&1; then
  echo "  sample app not found — run 'make cluster'." >&2
  exit 1
fi
echo "  kind cluster + sample app: up"

if kubectl get clusterpolicy require-approved-image >/dev/null 2>&1; then
  echo "  Kyverno policy: active"
else
  echo "  Kyverno policy not found — run 'make kyverno' for the full demo (continuing)."
fi

echo "building binaries..."
go build -o bin/worker ./cmd/worker
go build -o bin/starter ./cmd/starter

echo
echo "=== durability proof ==="
exec ./scripts/chaos/kill-worker.sh
