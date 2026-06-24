.PHONY: deps server worker hello tidy fmt vet clean

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
	go run ./cmd/starter --name "$(or $(NAME),world)"

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf audit/*.db audit/*.jsonl
