# TemporalOps

A self-healing Kubernetes deploy/rollback orchestrator built on
[Temporal](https://temporal.io). It runs a progressive canary release as a
durable workflow: policy check, canary scale-up, health bake, traffic shift,
human approval gate, and full promotion — with hand-written saga compensation
that rolls the change back in reverse order when any step fails or approval
times out.

The goal of the project is to demonstrate fault-tolerant orchestration: the
workflow survives worker crashes, K8s/Kyverno API timeouts, and missing
approvals without losing state or duplicating side effects, and every step is
recorded to a queryable audit log.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the design and rationale.

## Status

Built incrementally. Current stage: **3 — real Kubernetes against a kind cluster**.

| Stage | Scope | Done |
|------:|-------|:----:|
| 1 | Temporal dev server + hello-world workflow | ✅ |
| 2 | CanaryDeployWorkflow with mocked activities (saga, signal gate, timeout) | ✅ |
| 3 | Real K8s API calls against a kind cluster | ✅ |
| 4 | Kyverno policy check (signed/scanned image gate) | ✅ |
| 4 | Kyverno policy check | |
| 5 | ReleaseOrchestratorWorkflow child fan-out | |
| 6 | Append-only audit log | |
| 7 | Chaos / fault-injection scripts | |
| 8 | Prometheus + Grafana | |
| 9 | Demo script + architecture diagram | |

## Prerequisites

- Go 1.25+
- [Temporal CLI](https://docs.temporal.io/cli) (`brew install temporal`)
- Docker, kubectl, kind (used from Stage 3 onward)

## Stage 1: run the hello-world workflow

Three terminals.

```sh
# 1. Temporal dev server (Web UI on http://localhost:8233)
make server

# 2. Worker — registers workflows/activities, polls the task queue
make worker

# 3. Start the workflow and print its result
make hello NAME=temporalops
```

Expected output from the starter:

```
started workflow id=hello-... run=...
result: hello, temporalops
```

### Verify

- The starter prints `result: hello, temporalops`.
- Open http://localhost:8233, select the `hello-...` workflow, and confirm the
  event history shows `WorkflowExecutionStarted`, the `Greet` activity
  scheduled/started/completed, and `WorkflowExecutionCompleted`.

The dev server runs in-memory by default, so state resets when you stop it.
Durable-execution demos (Stage 7) document how to persist across restarts.

## Stage 2: canary deploy workflow

`CanaryDeployWorkflow` runs the full release sequence with **mocked** activities
(no real Kubernetes yet — that lands in Stage 3): policy gate, canary scale-up,
health bake, traffic shift, human approval gate, and promotion, with a
hand-written saga that rolls back in reverse order on any failure.

Key behaviours, all covered by unit tests in
`internal/workflows/canary_test.go` (run `make test`, no infra needed):

| Scenario | Outcome |
|----------|---------|
| All steps pass, promotion approved | `Promoted` |
| Image fails the policy gate | `PolicyRejected` (no compensation — nothing changed yet) |
| Canary bakes unhealthy | `RolledBack` (canary scaled back down) |
| Traffic shifted, no approval before timeout | `TimedOut` (traffic shifted back, then scaled down) |
| Promotion explicitly rejected | `RolledBack` |

### Run it against the dev server

With the dev server (`make server`) and worker (`make worker`) running:

```sh
# Start a canary. Prints the workflow ID and the approve command to copy.
make canary SERVICE=web TAG=v2 BAKE=15 APPROVAL=2m

# Watch its phase advance (policy-check -> ... -> awaiting-approval)
make status ID=<workflow-id>

# Approve the promotion (recorded as the actor, for the Stage 6 audit log)
make approve ID=<workflow-id> ACTOR=alice
```

Inject failures to watch the saga roll back. `EXTRA` is passed through to the
starter:

```sh
make canary SERVICE=api EXTRA="--fail-health"    # -> RolledBack
make canary SERVICE=db  EXTRA="--fail-policy"    # -> PolicyRejected
make canary SERVICE=cache APPROVAL=15s           # don't approve -> TimedOut
```

Open the workflow in the Web UI (http://localhost:8233) to see the saga
compensations and the AlertActivity in the event history.

#### Design notes (Stage 2)

- A rollback or timeout is returned as a **normal workflow result** with a
  `RolledBack` / `TimedOut` status, not a workflow error — the workflow
  succeeded at its job of deploying safely. Only an unrecoverable infra error
  after retries fails the workflow.
- The saga compensation stack is hand-written (`internal/workflows/saga.go`),
  not a library, so the rollback ordering is explicit. Compensations run on a
  disconnected context so they complete even during cancellation.
- Every activity has an explicit `RetryPolicy`; the rationale for each choice is
  commented at the definition in `internal/workflows/canary.go`.

## Stage 3: real Kubernetes (kind)

The activities now drive a real cluster via `client-go` instead of returning
mocked results. The workflow, saga, and signal logic are unchanged — only the
activity internals (`internal/activities/k8s_client.go` and the scale/health/
traffic/promote activities) became real.

**Traffic model (replica-ratio):** each service maps to two Deployments,
`<svc>-stable` and `<svc>-canary`, behind a single Service. Shifting traffic
means adjusting the replica split; promotion rolls the new image onto stable and
retires the canary. The pod readiness probe is the "synthetic health endpoint"
the HealthCheck activity observes.

### Setup

```sh
make cluster        # kind create + deploy sample app (web-stable x3, web-canary x0)
make server         # terminal 2
make worker         # terminal 3 — uses your current kubecontext (kind-temporalops)
```

### Demo

```sh
# Healthy rollout: canary comes up, bakes, you approve, stable adopts the image.
make canary SERVICE=web TAG=nginx:1.27-alpine BAKE=15 APPROVAL=2m
make approve ID=<workflow-id> ACTOR=alice
kubectl -n temporalops get deploy -l app=web        # web-stable now runs the new image

# Unhealthy rollout: a bad image never becomes Ready, so the bake fails and the
# saga scales the canary back down — stable is never touched.
make canary SERVICE=web TAG=nginx:does-not-exist BAKE=10
kubectl -n temporalops get pods -l role=canary      # ErrImagePull during bake
# -> workflow result: RolledBack ("canary N/M replicas ready after bake")

make cluster-reset  # return the sample app to baseline
```

Verified end to end against kind: a healthy deploy promotes (stable image
swapped to the new tag), and an unhealthy deploy (bad image, pods stuck in
`ErrImagePull`) is detected at the bake and rolled back with no change to
stable.

> Idempotency: `scaleDeployment` performs no write when the desired replica
> count already matches observed state, and `setImage` re-applies the same image
> as a no-op — so an activity retry or a post-crash re-run (Stage 7) produces no
> duplicate side effect.

## Stage 4: Kyverno policy gate

`PolicyCheck` is now backed by a real Kyverno `ClusterPolicy`
(`deploy/kyverno/require-approved-image.yaml`). The policy admits only images
from the approved registry (`nginx:*`) into the `temporalops` namespace — a
stand-in for a signed/scanned-image gate. The activity dry-run-applies the
candidate image to the canary Deployment, so the API server runs it through
Kyverno's admission webhook without persisting anything; a denial is a failed
gate.

```sh
make kyverno     # install Kyverno + apply the policy (server-side apply)
```

```sh
# Unapproved image -> denied at admission -> workflow aborts, no compensation
make canary SERVICE=web TAG=busybox:1.36
# -> PolicyRejected ("admission webhook ... denied the request ... require-approved-image")

# Approved image -> admitted, deploy proceeds to the bake/approval gate
make canary SERVICE=web TAG=nginx:1.27-alpine
```

A policy rejection is deterministic, so it returns `PolicyRejected` with **no
saga compensation** — nothing was changed. The activity distinguishes a policy
denial (a 4xx admission rejection → reject) from an infra failure (network or
5xx → retryable error), so a flaky API server retries rather than failing the
deploy. Verified against kind: `busybox:1.36` is rejected by Kyverno;
`nginx:1.27-alpine` is admitted and promotes.

## Layout

```
cmd/worker      Temporal worker process
cmd/starter     CLI to start workflows and send signals
internal/       workflows, activities (and later: audit, config)
deploy/         compose, k8s manifests, kyverno, observability (later stages)
scripts/        cluster setup, demo, chaos (later stages)
```
