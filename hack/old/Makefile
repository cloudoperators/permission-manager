# pm-controller Makefile

# Image URL to use all building/pushing image targets
IMG ?= pm-controller:latest

# Go parameters
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

.PHONY: lint
lint: ## Run golangci-lint.
	golangci-lint run ./...

##@ Build

.PHONY: build
build: fmt vet ## Build all controller binaries.
	go build -o bin/pm-controller-kubernetes ./cmd/kubernetes/
	go build -o bin/pm-controller-aggregator ./cmd/aggregator/
	go build -o bin/pm-controller-github ./cmd/github/
	go build -o bin/pm-controller-greenhouse-teams ./cmd/greenhouse-teams/

.PHONY: run
run: fmt vet ## Run the kubernetes controller from your host.
	go run ./cmd/kubernetes/

.PHONY: docker-build
docker-build: ## Build docker image.
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image.
	docker push ${IMG}

##@ Deployment

.PHONY: helm-template
helm-template: ## Render Helm chart templates locally.
	helm template pm-controller chart/pm-controller

.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf bin/ cover.out

##@ E2E Testing

E2E_PARALLEL ?= 10

.PHONY: e2e
e2e: ## Run end-to-end tests (creates kind clusters, deploys controller, runs tests, cleans up).
	go test -tags=e2e -v -timeout 40m ./test/e2e/

.PHONY: e2e-parallel
e2e-parallel: ## Run e2e tests in parallel (default: 8 concurrent tests). Override with E2E_PARALLEL=N.
	go test -tags=e2e -v -timeout 20m -parallel $(E2E_PARALLEL) ./test/e2e/

.PHONY: e2e-keep
e2e-keep: ## Run e2e tests and keep clusters alive for debugging.
	E2E_KEEP_CLUSTERS=1 go test -tags=e2e -v -timeout 40m ./test/e2e/

.PHONY: e2e-keep-parallel
e2e-keep-parallel: ## Run e2e tests in parallel and keep clusters alive for debugging.
	E2E_KEEP_CLUSTERS=1 go test -tags=e2e -v -timeout 20m -parallel $(E2E_PARALLEL) ./test/e2e/

.PHONY: e2e-cleanup
e2e-cleanup: ## Delete all pm-e2e-* kind clusters.
	@echo "Deleting all pm-e2e-* kind clusters..."
	@kind delete cluster --name pm-e2e-mgmt 2>/dev/null || true
	@kind delete cluster --name pm-e2e-consumer-1 2>/dev/null || true
	@kind delete cluster --name pm-e2e-consumer-2 2>/dev/null || true
	@kind delete cluster --name pm-e2e-consumer-3 2>/dev/null || true
	@kind delete cluster --name pm-e2e-consumer-4 2>/dev/null || true
	@echo "Done."
