#!/usr/bin/env bash
# Verification suite: checks the whole project is built and wired correctly.
#
# Tiers:
#   STATIC  (always)  tooling, go build, go vet, gofmt, unit tests
#   LIVE    (auto)    runs against the kind cluster if it is reachable:
#                     every workflow outcome path, the audit log, the durability
#                     proof, and the metrics endpoint
#
# The LIVE tier manages its own Temporal dev server and worker so it has
# controllable PIDs. Stop any worker you started with `make worker` first, or
# the durability check cannot guarantee a single worker on the queue.
#
# Usage:
#   scripts/verify.sh              # static + live (if cluster up)
#   scripts/verify.sh --static     # static only
#   scripts/verify.sh --quick      # static + live but skip the ~90s durability test
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

MODE="full"
case "${1:-}" in
  --static) MODE="static" ;;
  --quick)  MODE="quick" ;;
  "")       MODE="full" ;;
  *) echo "usage: $0 [--static|--quick]" >&2; exit 2 ;;
esac

PASS=0
FAIL=0
SKIP=0
ok()   { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; FAIL=$((FAIL + 1)); }
skip() { printf '  \033[33mSKIP\033[0m  %s\n' "$1"; SKIP=$((SKIP + 1)); }
section() { printf '\n== %s ==\n' "$1"; }

have() { command -v "$1" >/dev/null 2>&1; }

# ---------------------------------------------------------------- STATIC tier
section "prerequisites"
for tool in go temporal docker kubectl kind; do
  if have "$tool"; then ok "tool present: $tool"; else bad "tool missing: $tool"; fi
done

section "static analysis + unit tests"
if go build ./... 2>/tmp/verify-build.log; then ok "go build"; else bad "go build"; cat /tmp/verify-build.log; fi
if go vet ./... 2>/tmp/verify-vet.log; then ok "go vet"; else bad "go vet"; cat /tmp/verify-vet.log; fi
if [ -z "$(gofmt -l . 2>/dev/null)" ]; then ok "gofmt (no unformatted files)"; else bad "gofmt: $(gofmt -l .)"; fi
if go test ./... >/tmp/verify-test.log 2>&1; then ok "go test ./... (unit suite)"; else bad "go test ./..."; tail -20 /tmp/verify-test.log; fi

if [ "$MODE" = "static" ]; then
  section "summary"
  printf 'passed=%d failed=%d skipped=%d\n' "$PASS" "$FAIL" "$SKIP"
  [ "$FAIL" -eq 0 ]
  exit $?
fi

# ------------------------------------------------------------------ LIVE tier
section "live preconditions"

LIVE=1
if ! kubectl config current-context 2>/dev/null | grep -q kind-temporalops; then
  skip "kubecontext is not kind-temporalops — run 'make cluster && make kyverno'"; LIVE=0
elif ! kubectl -n temporalops get deployment web-stable >/dev/null 2>&1; then
  skip "sample app not found — run 'make cluster'"; LIVE=0
else
  ok "kind cluster + sample app reachable"
fi

OTHER_WORKERS=$(ps -Ao comm | grep -c '/worker$' || true)
if [ "$LIVE" = "1" ] && [ "$OTHER_WORKERS" -gt 0 ]; then
  skip "another worker is running — stop it so the durability check is deterministic"; LIVE=0
fi

if [ "$LIVE" = "0" ]; then
  section "summary"
  printf 'passed=%d failed=%d skipped=%d\n' "$PASS" "$FAIL" "$SKIP"
  [ "$FAIL" -eq 0 ]
  exit $?
fi

# Managed server + worker.
DEV_PID=""
WK_PID=""
STARTED_SERVER=0
WORKER_LOG="$(mktemp -t verify-worker.XXXXXX)"

cleanup() {
  [ -n "$WK_PID" ] && kill -9 "$WK_PID" 2>/dev/null || true
  [ "$STARTED_SERVER" = "1" ] && [ -n "$DEV_PID" ] && kill -9 "$DEV_PID" 2>/dev/null || true
}
trap cleanup EXIT

start_worker() {
  ./bin/worker >"$WORKER_LOG" 2>&1 &
  WK_PID=$!
  sleep 3
}

result_status() {
  temporal workflow result --workflow-id "$1" 2>/dev/null | grep -o '"Status":"[^"]*"' | head -1 | cut -d'"' -f4
}
assert_status() {
  local id="$1" want="$2" name="$3"
  local got; got="$(result_status "$id")"
  if [ "$got" = "$want" ]; then ok "$name -> $got"; else bad "$name (got '${got:-<none>}', want '$want')"; fi
}

section "build binaries + start server/worker"
if go build -o bin/worker ./cmd/worker && go build -o bin/starter ./cmd/starter; then ok "build bin/worker bin/starter"; else bad "build binaries"; exit 1; fi

if lsof -iTCP:7233 -sTCP:LISTEN -n >/dev/null 2>&1; then
  ok "reusing existing Temporal dev server on :7233"
else
  temporal server start-dev --ui-port 8233 --headless >/tmp/verify-temporal.log 2>&1 &
  DEV_PID=$!; STARTED_SERVER=1
  sleep 7
  if temporal operator namespace list >/dev/null 2>&1; then ok "started Temporal dev server"; else bad "Temporal dev server did not come up"; exit 1; fi
fi
start_worker
if grep -q 'Started Worker' "$WORKER_LOG"; then ok "worker started and polling"; else bad "worker did not start"; cat "$WORKER_LOG"; fi

section "workflow outcome paths"
# Happy path: approved promotion.
HAPPY="verify-happy-$$"
./bin/starter canary --id "$HAPPY" --service web --tag nginx:1.27-alpine --bake 15 --approval-timeout 3m >/dev/null 2>&1
sleep 17
./bin/starter approve --id "$HAPPY" --actor verifier >/dev/null 2>&1
sleep 5
assert_status "$HAPPY" "Promoted" "happy path (approve)"

# Policy gate: unapproved image rejected by Kyverno.
POLICY="verify-policy-$$"
./bin/starter canary --id "$POLICY" --service web --tag busybox:1.36 --bake 5 --approval-timeout 1m --wait >/dev/null 2>&1
assert_status "$POLICY" "PolicyRejected" "policy gate (busybox denied)"

# Health rollback: bad image never becomes ready.
HEALTH="verify-health-$$"
./bin/starter canary --id "$HEALTH" --service web --tag nginx:does-not-exist-9999 --bake 12 --approval-timeout 2m >/dev/null 2>&1
sleep 16
assert_status "$HEALTH" "RolledBack" "health rollback (bad image)"

# Approval timeout: no signal sent.
TIMEOUT="verify-timeout-$$"
./bin/starter canary --id "$TIMEOUT" --service web --tag nginx:1.27-alpine --bake 8 --approval-timeout 12s >/dev/null 2>&1
sleep 26
assert_status "$TIMEOUT" "TimedOut" "approval timeout (no signal)"

# Multi-service fan-out.
if ./bin/starter release --services web,api --tag nginx:1.27-alpine --bake 8 2>&1 | grep -q 'allPromoted=true'; then
  ok "release fan-out (web,api both promoted)"
else
  bad "release fan-out did not report allPromoted=true"
fi

section "audit log"
if have sqlite3; then
  APPROVAL_ROWS=$(sqlite3 audit/audit.db "SELECT count(*) FROM audit_log WHERE workflow_id='$HAPPY' AND phase='approval' AND actor='verifier';" 2>/dev/null)
  if [ "${APPROVAL_ROWS:-0}" -ge 1 ]; then ok "audit approval row tagged actor=verifier"; else bad "audit approval row missing"; fi
  ROWS=$(sqlite3 audit/audit.db "SELECT count(*) FROM audit_log WHERE workflow_id='$HAPPY';" 2>/dev/null)
  if [ "${ROWS:-0}" -ge 8 ]; then ok "audit trail recorded ($ROWS rows for happy path)"; else bad "audit trail too short ($ROWS rows)"; fi
else
  if ./bin/starter audit --id "$HAPPY" 2>&1 | grep -q 'actor=verifier'; then ok "audit approval row tagged actor=verifier"; else bad "audit approval row missing"; fi
fi

section "metrics endpoint"
if curl -s "http://localhost:9090/metrics" | grep -q '^temporal_workflow_completed'; then
  ok "worker exposes temporal_workflow_completed on :9090/metrics"
else
  bad "metrics endpoint missing temporal_ series"
fi

if [ "$MODE" = "quick" ]; then
  section "durability"
  skip "durability proof skipped (--quick)"
else
  section "durability proof (kill worker mid-bake, ~90s)"
  DUR="verify-durability-$$"
  ./bin/starter canary --id "$DUR" --service web --tag nginx:1.27-alpine --bake 40 --approval-timeout 5m >/dev/null 2>&1
  sleep 8
  kill -9 "$WK_PID"; WK_PID=""        # crash mid-bake
  sleep 4
  start_worker                         # restart -> workflow must resume
  sleep 36
  ./bin/starter approve --id "$DUR" --actor chaos >/dev/null 2>&1
  sleep 5
  assert_status "$DUR" "Promoted" "workflow resumed after worker crash"
  if have sqlite3; then
    HC=$(sqlite3 audit/audit.db "SELECT count(*) FROM audit_log WHERE workflow_id='$DUR' AND activity_type='HealthCheck' AND phase='start';" 2>/dev/null)
    SC=$(sqlite3 audit/audit.db "SELECT count(*) FROM audit_log WHERE workflow_id='$DUR' AND activity_type='ScaleCanary' AND phase='end' AND status='completed';" 2>/dev/null)
    if [ "${HC:-0}" -ge 2 ]; then ok "HealthCheck retried after crash (start count=$HC)"; else bad "HealthCheck not retried (start count=$HC)"; fi
    if [ "${SC:-0}" -eq 1 ]; then ok "ScaleCanary executed exactly once (no duplicate side effect)"; else bad "ScaleCanary completion count=$SC (expected 1)"; fi
  fi
fi

section "summary"
printf 'passed=%d failed=%d skipped=%d\n' "$PASS" "$FAIL" "$SKIP"
if [ "$FAIL" -eq 0 ]; then
  printf '\033[32mVERIFICATION PASSED\033[0m\n'
else
  printf '\033[31mVERIFICATION FAILED\033[0m\n'
fi
[ "$FAIL" -eq 0 ]
