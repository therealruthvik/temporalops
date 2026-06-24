.PHONY: deps build server worker hello canary release approve status audit test cluster cluster-reset cluster-down kyverno chaos-kill-worker observe-up observe-down tidy fmt vet clean

# Start the Temporal dev server (in-memory) with the Web UI on :8233 and the
# frontend gRPC on :7233. Run this in its own terminal; leave it running.
server:
	temporal server start-dev --ui-port 8233

# Install/refresh Go module dependencies.
deps:
	go mod download

# Compile the binaries into ./bin. The chaos scripts run ./bin/worker directly
# (rather than `go run`, whose child binary outlives the parent on kill) so the
# worker has a controllable PID for the durability demo.
build:
	go build -o bin/worker ./cmd/worker
	go build -o bin/starter ./cmd/starter

tidy:
	go mod tidy

# Run the worker. Polls the temporalops task queue. Kill/restart this to see
# durable execution resume (Stage 7 chaos demo).
worker:
	go run ./cmd/worker

# Stage 1 smoke test: start HelloWorkflow and print its result.
hello:
	go run ./cmd/starter hello --name "$(or $(NAME),world)"

# Stage 2: start a canary deploy. Override knobs with vars, e.g.
#   make canary SERVICE=web TAG=v2 BAKE=15 APPROVAL=15m
# Add failure injection: EXTRA="--fail-health" (or --fail-policy/--fail-traffic).
canary:
	go run ./cmd/starter canary \
		--service "$(or $(SERVICE),web)" \
		--tag "$(or $(TAG),v2)" \
		--bake "$(or $(BAKE),15)" \
		--approval-timeout "$(or $(APPROVAL),15m)" $(EXTRA)

# Stage 5: multi-service release. Fans out one canary per service.
#   make release SERVICES=web,api TAG=nginx:1.27-alpine BAKE=15
release:
	go run ./cmd/starter release \
		--services "$(or $(SERVICES),web,api)" \
		--tag "$(or $(TAG),nginx:1.27-alpine)" \
		--bake "$(or $(BAKE),15)"

# Approve a waiting canary: make approve ID=<workflow-id> ACTOR=you
approve:
	go run ./cmd/starter approve --id "$(ID)" --actor "$(or $(ACTOR),operator)"

# Query a workflow's current phase: make status ID=<workflow-id>
status:
	go run ./cmd/starter status --id "$(ID)"

# Stage 6: print the append-only audit trail for a workflow.
#   make audit ID=<workflow-id>
audit:
	go run ./cmd/starter audit --id "$(ID)"

# Run the workflow unit tests (saga, signal gate, timeout — no infra needed).
test:
	go test ./...

# Stage 3: create the kind cluster and deploy the sample app (idempotent).
cluster:
	./scripts/setup-cluster.sh

# Stage 4: install Kyverno and apply the image policy (idempotent).
kyverno:
	./scripts/install-kyverno.sh

# Stage 7: durability proof — kill the worker mid-deploy and show the workflow
# resume on restart with no duplicate side effects. Requires `make server` and a
# cluster (make cluster && make kyverno) to be up.
chaos-kill-worker: build
	./scripts/chaos/kill-worker.sh

# Stage 8: bring up Prometheus + Grafana. Prometheus scrapes the worker's SDK
# metrics on :9090; Grafana (http://localhost:3000) is pre-provisioned with the
# data source and the TemporalOps dashboard.
observe-up:
	docker compose -f deploy/observability/docker-compose.yml up -d

observe-down:
	docker compose -f deploy/observability/docker-compose.yml down

# Reset the sample app to its baseline (stable image, canary at zero).
cluster-reset:
	kubectl apply -f deploy/k8s/sample-app.yaml
	kubectl -n temporalops rollout status deployment/web-stable --timeout=120s

# Delete the kind cluster.
cluster-down:
	kind delete cluster --name $(or $(CLUSTER),temporalops)

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf audit/*.db audit/*.jsonl
