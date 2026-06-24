.PHONY: deps server worker hello canary approve status test tidy fmt vet clean

# Start the Temporal dev server (in-memory) with the Web UI on :8233 and the
# frontend gRPC on :7233. Run this in its own terminal; leave it running.
server:
	temporal server start-dev --ui-port 8233

# Install/refresh Go module dependencies.
deps:
	go mod download

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

# Approve a waiting canary: make approve ID=<workflow-id> ACTOR=you
approve:
	go run ./cmd/starter approve --id "$(ID)" --actor "$(or $(ACTOR),operator)"

# Query a workflow's current phase: make status ID=<workflow-id>
status:
	go run ./cmd/starter status --id "$(ID)"

# Run the workflow unit tests (saga, signal gate, timeout — no infra needed).
test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf audit/*.db audit/*.jsonl
