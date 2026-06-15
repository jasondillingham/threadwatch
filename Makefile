.DEFAULT_GOAL := help

GO          ?= go
IMAGE_REPO  ?= ghcr.io/jasondillingham/threadwatch
TAG         ?= dev
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: help
help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n\nTargets:\n"} \
		/^[a-zA-Z0-9_.-]+:.*##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST)

.PHONY: build
build: ## Build the threadwatch binary into ./bin
	@mkdir -p bin
	$(GO) build -trimpath -o bin/threadwatch ./cmd/threadwatch

.PHONY: run
run: ## Run threadwatch locally with default config
	$(GO) run ./cmd/threadwatch

.PHONY: test
test: ## Run unit tests
	$(GO) test ./... -race -count=1

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint (requires golangci-lint installed)
	golangci-lint run ./...

.PHONY: docker
docker: ## Build the container image as $(IMAGE_REPO):$(TAG)
	docker build \
		--build-arg VERSION=$(TAG) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(IMAGE_REPO):$(TAG) \
		.

.PHONY: docker-push
docker-push: docker ## Push the container image (requires docker login ghcr.io)
	docker push $(IMAGE_REPO):$(TAG)

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart
	helm lint charts/threadwatch

.PHONY: helm-template
helm-template: ## Render the Helm chart for visual inspection
	helm template threadwatch charts/threadwatch
