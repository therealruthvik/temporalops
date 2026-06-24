#!/usr/bin/env bash
# Durability proof: kill the worker mid-deploy and show the workflow resume on
# restart from its last completed step, with no duplicate side effects.
#
# Requires:
#   - the Temporal dev server running (make server)
#   - the kind cluster + Kyverno up (make cluster && make kyverno)
#   - binaries built (make build) — the Makefile target does this for you
#
# The worker is killed during the health bake. On restart Temporal replays the
# workflow history: completed activities (PolicyCheck, ScaleCanary) are NOT
# re-run, and the in-flight HealthCheck is retried. Because the activities are
# idempotent, the retry produces no extra side effect. The workflow then
# proceeds to promotion as if nothing happened.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

WORKER_BIN="./bin/worker"
STARTER_BIN="./bin/starter"
WORKER_LOG="$(mktemp -t temporalops-worker.XXXXXX)"
WORKER_PID=""

start_worker() {
  "$WORKER_BIN" >"$WORKER_LOG" 2>&1 &
  WORKER_PID=$!
  echo "  worker started (pid $WORKER_PID), log: $WORKER_LOG"
  sleep 3
}

cleanup() {
  [ -n "$WORKER_PID" ] && kill -9 "$WORKER_PID" 2>/dev/null || true
}
trap cleanup EXIT

echo "== 1. start worker =="
start_worker

echo "== 2. start a canary with a long bake so we can crash it mid-flight =="
WFID="canary-chaos-$(date +%s)"
"$STARTER_BIN" canary --id "$WFID" --service web --tag nginx:1.27-alpine \
  --bake 40 --approval-timeout 5m >/tmp/chaos-start.log 2>&1
echo "  workflow: $WFID"
sleep 6
echo "  phase before crash: $("$STARTER_BIN" status --id "$WFID" 2>&1 | grep -o 'phase: .*')"

echo "== 3. KILL the worker mid-bake =="
kill -9 "$WORKER_PID"
WORKER_PID=""
echo "  worker killed; the workflow is now stranded with no worker to run it"
sleep 4

echo "== 4. restart the worker — workflow should resume =="
start_worker
sleep 3
echo "  phase after restart: $("$STARTER_BIN" status --id "$WFID" 2>&1 | grep -o 'phase: .*')"

echo "== 5. let the bake finish, then approve =="
sleep 36
"$STARTER_BIN" approve --id "$WFID" --actor chaos-operator 2>&1 | grep -o 'approved.*' || true
sleep 4

echo "== 6. result =="
temporal workflow result --workflow-id "$WFID" 2>/dev/null | grep -i result || true

echo
echo "== 7. audit trail (note: ScaleCanary recorded once; HealthCheck retried after the crash) =="
"$STARTER_BIN" audit --id "$WFID" 2>&1 | grep -v 'No logger'

echo
echo "Open http://localhost:8233 and inspect $WFID to see the full event history,"
echo "including the WorkerDown / ActivityTaskTimedOut and the resumed execution."
