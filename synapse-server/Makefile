.PHONY: help tidy fmt fmt-check vet lint build build-release test test-cover test-pg run migrate clean

BINARY_NAME := synapse-server
BINARY_OUT  := bin/$(BINARY_NAME)
CONFIG_FILE ?= configs/config.example.yaml
UI_DIR      ?= internal/ui/static

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make <target>\n\n"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tidy: ## Synchronize go.mod and go.sum
	go mod tidy

fmt: ## Format all Go source files with gofmt
	gofmt -w .

fmt-check: ## Verify all Go files are formatted (CI hard gate)
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then \
		echo "Unformatted files:"; \
		echo "$$out"; \
		exit 1; \
	fi

vet: ## Run go vet across all packages
	go vet ./...

lint: ## Run golangci-lint if installed
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed; run 'make vet' instead"; exit 1; }
	golangci-lint run ./...

build: ## Compile all packages
	go build ./...

build-release: ## Build single static zero-CGO release binary
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -o $(BINARY_OUT) ./cmd/synapse-server

test: ## Run full test suite with race detector (includes arch_test & guard_test)
	go test -race ./...

test-cover: ## Run tests with race detector and coverage summary
	go test -race -cover ./...

test-pg: ## Run PostgreSQL 17 integration tests (requires TEST_POSTGRES_DSN)
	@if [ -z "$(TEST_POSTGRES_DSN)" ]; then \
		echo "TEST_POSTGRES_DSN is required (e.g. postgres://synapse:synapse@127.0.0.1:5432/synapse?sslmode=disable&TimeZone=UTC)"; \
		exit 1; \
	fi
	TEST_POSTGRES_DSN="$(TEST_POSTGRES_DSN)" go test -race ./internal/infra/db/... ./internal/storage/postgres/...

check: fmt-check vet build test ## Run all mandatory pre-merge hard gates

run: ## Run server in local dev mode with live UI directory
	go run ./cmd/synapse-server serve --config $(CONFIG_FILE) --ui-dir $(UI_DIR)

migrate: ## Run embedded goose PostgreSQL migrations
	go run ./cmd/synapse-server migrate --config $(CONFIG_FILE)

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out coverage.html
