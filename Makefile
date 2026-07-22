# Licensed to the Apache Software Foundation (ASF) under one
# or more contributor license agreements.  See the NOTICE file
# distributed with this work for additional information
# regarding copyright ownership.  The ASF licenses this file
# to you under the Apache License, Version 2.0 (the
# "License"); you may not use this file except in compliance
# with the License.  You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

# ==============================================================================
# Shell
# ==============================================================================

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

# ==============================================================================
# Go environment
# ==============================================================================

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN = $(shell go env GOPATH)/bin
else
GOBIN = $(shell go env GOBIN)
endif

# YEAR defines the year value used for substituting the YEAR placeholder in the boilerplate header.
YEAR ?= $(shell date +%Y)

# ==============================================================================
# Image / container
# ==============================================================================

# Image URL to use all building/pushing image targets
IMG ?= pm/manager:local
IMG_LICENSE_EYE ?= ghcr.io/apache/skywalking-eyes/license-eye

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1).
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le

# PLATFORM is the single-arch target for `docker-build`. It is auto-detected from
# the host architecture but can be overridden, e.g. `PLATFORM=linux/arm64 make docker-build`.
UNAME_P := $(shell uname -p)
ifeq ($(UNAME_P),x86_64)
    HOST_PLATFORM := linux/amd64
else ifneq ($(filter arm%,$(UNAME_P)),)
    HOST_PLATFORM := linux/arm64
else
    HOST_PLATFORM := linux/amd64
endif
PLATFORM ?= $(HOST_PLATFORM)

# ==============================================================================
# E2E
# ==============================================================================

# kubectl kuberc is disabled by default for test isolation; enable with:
# - KUBECTL_KUBERC=true
# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER ?= greenhouse-admin

# ==============================================================================
# Deployment
# ==============================================================================

ifndef ignore-not-found
  ignore-not-found = false
endif

# ==============================================================================
# Tool binary locations
# ==============================================================================

# Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin

KUBECTL        ?= kubectl
KIND           ?= kind
KUSTOMIZE      ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST        ?= $(LOCALBIN)/setup-envtest
GOIMPORTS      ?= $(LOCALBIN)/goimports
GINKGO         ?= $(LOCALBIN)/ginkgo
GOLANGCI_LINT   = $(LOCALBIN)/golangci-lint

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# ==============================================================================
# Tool versions
# ==============================================================================

KUSTOMIZE_VERSION        ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.20.1
GOLANGCI_LINT_VERSION    ?= v2.12.2
GINKGO_VERSION           ?= $(call gomodver,github.com/onsi/ginkgo/v2)

# ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')

# ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

# ==============================================================================
# Includes
# ==============================================================================

include hack/setup.mk

# ==============================================================================
# Targets
# ==============================================================================

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate CRD and RBAC manifests.
	"$(CONTROLLER_GEN)" crd paths="./..." output:crd:artifacts:config=charts/permission-manager/crds
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role paths="./..." output:artifacts:config=bin/rbac
	{ head -n 11 charts/permission-manager/templates/rbac/manager-role.yaml; yq '{"rules": .rules}' bin/rbac/role.yaml; } > bin/rbac/manager-role-new.yaml
	mv bin/rbac/manager-role-new.yaml charts/permission-manager/templates/rbac/manager-role.yaml

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="./..."

# Install mockery locally (e.g. `go install github.com/vektra/mockery/v2@latest`).
MOCKERY ?= mockery
.PHONY: mockery
mockery: ## Generate mocks per .mockery.yaml configuration.
	@command -v $(MOCKERY) >/dev/null || (echo "mockery not found; install it first" && exit 1)
	$(MOCKERY) --config .mockery.yaml
.PHONY: goimports
goimports: $(GOIMPORTS)
$(GOIMPORTS): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install golang.org/x/tools/cmd/goimports@latest

.PHONY: fmt
fmt: goimports
	GOBIN=$(LOCALBIN) go fmt ./...
	$(GOIMPORTS) -w -local github.com/cloudoperators/permission-manager .

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

E2E_PROCS ?= 4
E2E_CONTROLLER_NAMESPACE ?= greenhouse
E2E_CONTROLLER_DEPLOYMENT ?= permission-manager-controller-manager
.PHONY: setup-e2e
setup-e2e: ## Provision the dev environment and deploy the controller for e2e.
	$(MAKE) setup
	$(MAKE) demo
	$(MAKE) docker-build
	$(MAKE) kind-load
	$(MAKE) deploy HELM_EXTRA_ARGS="--set providers.github=true"
	"$(KUBECTL)" --context kind-$(ADMIN_CLUSTER) -n $(E2E_CONTROLLER_NAMESPACE) rollout restart deployment $(E2E_CONTROLLER_DEPLOYMENT)
	"$(KUBECTL)" --context kind-$(ADMIN_CLUSTER) -n $(E2E_CONTROLLER_NAMESPACE) rollout status deployment $(E2E_CONTROLLER_DEPLOYMENT) --timeout=120s

.PHONY: e2e
e2e: ginkgo ## Run the e2e tests against the environment provisioned by setup-e2e.
	E2E_ADMIN_CONTEXT=kind-$(ADMIN_CLUSTER) E2E_REMOTE_CONTEXT=kind-$(REMOTE_CLUSTER) E2E_REMOTE_CLUSTER=kind-$(REMOTE_CLUSTER) \
	E2E_CONTROLLER_NAMESPACE=$(E2E_CONTROLLER_NAMESPACE) E2E_CONTROLLER_DEPLOYMENT=$(E2E_CONTROLLER_DEPLOYMENT) \
		$(GINKGO) --procs=$(E2E_PROCS) --tags=e2e -v --json-report=$(LOCALBIN)/e2e-report.json ./e2e/

.PHONY: clean-e2e
clean-e2e: ## Delete the e2e Kind clusters.
	$(KIND) delete cluster --name $(ADMIN_CLUSTER)
	$(KIND) delete cluster --name $(REMOTE_CLUSTER)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run --timeout=5m

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix --timeout=5m

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build --platform=$(PLATFORM) -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# To use docker-buildx you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name kubebuilder-scaffold-builder
	$(CONTAINER_TOOL) buildx use kubebuilder-scaffold-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm kubebuilder-scaffold-builder
	rm Dockerfile.cross

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart.
	helm lint charts/permission-manager

##@ Deployment

.PHONY: install
install: ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	"$(KUBECTL)" --context kind-$(KIND_CLUSTER) apply --server-side --force-conflicts -f charts/permission-manager/crds

.PHONY: uninstall
uninstall: ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	"$(KUBECTL)" --context kind-$(KIND_CLUSTER) delete --ignore-not-found=$(ignore-not-found) -f charts/permission-manager/crds

.PHONY: deploy
deploy: manifests ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	helm template permission-manager charts/permission-manager --namespace greenhouse --include-crds \
		--set manager.image.repository=$(word 1,$(subst :, ,$(IMG))) --set manager.image.tag=$(word 2,$(subst :, ,$(IMG))) \
		$(HELM_EXTRA_ARGS) \
		| "$(KUBECTL)" --context kind-$(KIND_CLUSTER) apply --server-side --force-conflicts -f -

.PHONY: kind-load
kind-load: ## Load docker image into the Kind cluster.
	$(KIND) load docker-image ${IMG} --name $(KIND_CLUSTER)

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	helm template permission-manager charts/permission-manager --namespace greenhouse --skip-crds \
		| "$(KUBECTL)" --context kind-$(KIND_CLUSTER) delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: ginkgo
ginkgo: $(GINKGO) ## Download ginkgo locally if necessary.
$(GINKGO): $(LOCALBIN)
	$(call go-install-tool,$(GINKGO),github.com/onsi/ginkgo/v2/ginkgo,$(GINKGO_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	@test -f .custom-gcl.yml && { \
		echo "Building custom golangci-lint with plugins..." && \
		$(GOLANGCI_LINT) custom --destination $(LOCALBIN) --name golangci-lint-custom && \
		mv -f $(LOCALBIN)/golangci-lint-custom $(GOLANGCI_LINT); \
	} || true

.PHONY: license
license:
	docker run --rm -v $(shell pwd):/github/workspace $(IMG_LICENSE_EYE) -c .github/licenserc.yaml header fix

# ==============================================================================
# Helper functions
# ==============================================================================

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
