#!/usr/bin/env bash
# Fault injection: drive the workflow down each failure path and show how it
# responds. Requires the dev server, worker, cluster, and Kyverno to be running.
#
# Usage: scripts/chaos/inject.sh <k8s|kyverno|signal>
#
#   k8s      simulate a Kubernetes API failure during the traffic shift
#            (ShiftTraffic errors -> retries exhausted -> saga rollback)
#   kyverno  simulate Kyverno blocking the image
#            (PolicyCheck rejects -> workflow aborts early, no compensation)
#   signal   the approval signal never arrives
#            (approval timeout -> auto-rollback, TimedOut)
#
# The --fail-* flags inject the failure deterministically; the workflow's
# response is identical whether the underlying dependency timed out or errored.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

MODE="${1:-}"
STARTER="go run ./cmd/starter"
WFID="canary-chaos-${MODE}-$(date +%s)"

case "$MODE" in
  k8s)
    echo "injecting K8s API failure during traffic shift -> expect RolledBack"
    $STARTER canary --id "$WFID" --service web --tag nginx:1.27-alpine \
      --bake 5 --approval-timeout 2m --fail-traffic --wait
    ;;
  kyverno)
    echo "injecting Kyverno rejection -> expect PolicyRejected (no compensation)"
    $STARTER canary --id "$WFID" --service web --tag nginx:1.27-alpine \
      --bake 5 --approval-timeout 2m --fail-policy --wait
    ;;
  signal)
    echo "withholding the approval signal -> expect TimedOut after 15s"
    $STARTER canary --id "$WFID" --service web --tag nginx:1.27-alpine \
      --bake 5 --approval-timeout 15s --wait
    ;;
  *)
    echo "usage: $0 <k8s|kyverno|signal>" >&2
    exit 2
    ;;
esac

echo
echo "audit trail:"
$STARTER audit --id "$WFID" 2>&1 | grep -v 'No logger'
