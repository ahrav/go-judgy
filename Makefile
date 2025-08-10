# Go project Makefile for go-judgy
# Temporal workflow orchestration platform

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt
GOVET=$(GOCMD) vet

# Binary names and paths
WORKER_BINARY=worker
CLI_BINARY=cli
BINARY_DIR=bin

# Build flags
LDFLAGS=-ldflags "-w -s"
RACE_FLAGS=-race

# Test flags
TEST_FLAGS=-v
COVERAGE_FLAGS=-coverprofile=coverage.out -covermode=atomic

# Linting
GOLANGCI_LINT=golangci-lint
GOLANGCI_LINT_VERSION=v1.64.8

# Kind cluster
KIND_CLUSTER_NAME=go-judgy-local
KIND_CONFIG=deployments/kind/cluster.yaml

.PHONY: help
help: ## Display this help message
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build all binaries (worker and cli)
	@echo "Building binaries..."
	@mkdir -p $(BINARY_DIR)
	@$(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(WORKER_BINARY) ./cmd/worker
	@$(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(CLI_BINARY) ./cmd/cli
	@echo "Binaries built successfully"

.PHONY: test
test: ## Run unit tests (excludes integration tests)
	@echo "Running unit tests..."
	@GOEXPERIMENT=synctest $(GOTEST) $(TEST_FLAGS) -tags='!integration' ./...

.PHONY: test-race
test-race: ## Run unit tests with race detection (excludes integration tests)
	@echo "Running unit tests with race detection..."
	@GOEXPERIMENT=synctest $(GOTEST) $(TEST_FLAGS) $(RACE_FLAGS) -tags='!integration' ./...

.PHONY: test-coverage
test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	@GOEXPERIMENT=synctest $(GOTEST) $(TEST_FLAGS) $(COVERAGE_FLAGS) -tags='!integration' ./...
	@$(GOCMD) tool cover -func=coverage.out
	@$(GOCMD) tool cover -html=coverage.out -o coverage.html

.PHONY: test-integration
test-integration: ## Run integration tests (includes Docker containers)
	@echo "Running integration tests..."
	@GOEXPERIMENT=synctest $(GOTEST) $(TEST_FLAGS) -tags=integration ./...

.PHONY: test-all
test-all: ## Run all tests (unit + integration)
	@echo "Running all tests..."
	@GOEXPERIMENT=synctest $(GOTEST) $(TEST_FLAGS) ./...


.PHONY: lint
lint: ## Run golangci-lint
	@echo "Running linter..."
	@$(GOLANGCI_LINT) run ./...

.PHONY: pre-commit
pre-commit: fmt vet lint test-race ## Run all pre-commit checks (tests with race detection, linting)
	@echo "All pre-commit checks passed ✅"

.PHONY: fmt
fmt: ## Format Go code
	@echo "Formatting code..."
	@$(GOFMT) ./...

.PHONY: vet
vet: ## Run go vet
	@echo "Running go vet..."
	@$(GOVET) ./...

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning..."
	@$(GOCLEAN)
	@rm -rf $(BINARY_DIR)
	@rm -f coverage.out coverage.html

.PHONY: install-tools
install-tools: ## Install required development tools
	@echo "Installing development tools..."
	@$(GOCMD) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@echo "Tools installed successfully"

.PHONY: install-hooks
install-hooks: install-tools ## Setup pre-commit hooks
	@echo "Setting up pre-commit hooks..."
	@if [ -d .git ]; then \
		echo "#!/bin/bash" > .git/hooks/pre-commit; \
		echo "set -e" >> .git/hooks/pre-commit; \
		echo "echo 'Running pre-commit checks...'" >> .git/hooks/pre-commit; \
		echo "make pre-commit" >> .git/hooks/pre-commit; \
		chmod +x .git/hooks/pre-commit; \
		echo "Pre-commit hooks installed successfully"; \
	else \
		echo "Not a git repository - skipping hook installation"; \
	fi

.PHONY: deps
deps: ## Download and tidy dependencies
	@echo "Downloading dependencies..."
	@$(GOMOD) download
	@$(GOMOD) tidy

.PHONY: cluster
cluster: ## Create kind cluster
	@echo "Creating kind cluster..."
	@./scripts/setup-kind.sh

.PHONY: cluster-delete
cluster-delete: ## Delete kind cluster
	@echo "Deleting kind cluster..."
	@kind delete cluster --name $(KIND_CLUSTER_NAME)
	@docker rm -f kind-registry 2>/dev/null || true

.PHONY: deploy
deploy: build ## Deploy to Kubernetes cluster
	@echo "Deploying to Kubernetes cluster..."
	@kubectl apply -k deployments/k8s/base
	@kubectl apply -k deployments/k8s/worker

.PHONY: docker-build
docker-build: ## Build Docker images
	@echo "Building Docker images..."
	@docker build -f deployments/docker/Dockerfile.worker -t go-judgy-worker:latest .
	@docker build -f deployments/docker/Dockerfile.cli -t go-judgy-cli:latest .

.PHONY: run-worker
run-worker: build ## Run worker locally
	@echo "Running worker..."
	@./$(BINARY_DIR)/$(WORKER_BINARY)

.PHONY: run-cli
run-cli: build ## Run CLI locally
	@echo "Running CLI..."
	@./$(BINARY_DIR)/$(CLI_BINARY)

.PHONY: dev
dev: install-tools deps ## Setup development environment
	@echo "Development environment setup complete"

# Default target
.DEFAULT_GOAL := help