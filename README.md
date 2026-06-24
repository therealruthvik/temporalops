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

Built incrementally. Current stage: **1 — Temporal dev server + hello-world**.

| Stage | Scope | Done |
|------:|-------|:----:|
| 1 | Temporal dev server + hello-world workflow | ✅ |
| 2 | CanaryDeployWorkflow with mocked activities (saga, signal gate, timeout) | |
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

## Layout

```
cmd/worker      Temporal worker process
cmd/starter     CLI to start workflows and send signals
internal/       workflows, activities (and later: audit, config)
deploy/         compose, k8s manifests, kyverno, observability (later stages)
scripts/        cluster setup, demo, chaos (later stages)
```
