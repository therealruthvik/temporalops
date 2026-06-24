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

Built incrementally. Current stage: **2 — CanaryDeployWorkflow (mocked activities)**.

| Stage | Scope | Done |
|------:|-------|:----:|
| 1 | Temporal dev server + hello-world workflow | ✅ |
| 2 | CanaryDeployWorkflow with mocked activities (saga, signal gate, timeout) | ✅ |
| 3 | Real K8s API calls against a kind cluster | |
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

#### Design notes

- A rollback or timeout is returned as a **normal workflow result** with a
  `RolledBack` / `TimedOut` status, not a workflow error — the workflow
  succeeded at its job of deploying safely. Only an unrecoverable infra error
  after retries fails the workflow.
- The saga compensation stack is hand-written (`internal/workflows/saga.go`),
  not a library, so the rollback ordering is explicit. Compensations run on a
  disconnected context so they complete even during cancellation.
- Every activity has an explicit `RetryPolicy`; the rationale for each choice is
  commented at the definition in `internal/workflows/canary.go`.

## Layout

```
cmd/worker      Temporal worker process
cmd/starter     CLI to start workflows and send signals
internal/       workflows, activities (and later: audit, config)
deploy/         compose, k8s manifests, kyverno, observability (later stages)
scripts/        cluster setup, demo, chaos (later stages)
```
