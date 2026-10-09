IMG ?= controller:latest
CONTROLLER_GEN ?= controller-gen
KUSTOMIZE ?= kustomize
GOPROXY ?= $(shell go env GOPROXY)

.DEFAULT_GOAL := build

.PHONY: help
help: ## Show available targets.
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make <target>\n\n"} /^[a-zA-Z_0-9-]+:.*?##/ {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: tools
tools: ## Check required local development tools.
	@command -v $(CONTROLLER_GEN) >/dev/null || { echo "controller-gen is required" >&2; exit 1; }
	@command -v $(KUSTOMIZE) >/dev/null || { echo "kustomize is required" >&2; exit 1; }

.PHONY: manifests
manifests: tools ## Generate CRD, RBAC, and webhook manifests.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: tools ## Generate DeepCopy implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Format Go sources.
	gofmt -w $$(find . -name '*.go' -type f)

.PHONY: vet
vet: ## Run static checks.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet ## Run unit tests.
	go test ./...

.PHONY: build
build: generate fmt vet ## Build the controller.
	go build -o bin/manager main.go

.PHONY: docker-build
docker-build: ## Build the controller image.
	docker build --build-arg GOPROXY=$(GOPROXY) -t $(IMG) .

.PHONY: docker-push
docker-push: ## Push the controller image.
	docker push $(IMG)

.PHONY: install
install: manifests ## Install CRD and PriorityClasses into the active cluster.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -
	$(KUSTOMIZE) build config/scheduler | kubectl apply -f -

.PHONY: deploy
deploy: manifests ## Deploy with IMAGE=$(IMG), including webhook TLS.
	IMAGE=$(IMG) bash scripts/deploy.sh

.PHONY: verify
verify: ## Verify the deployment in the active cluster.
	bash scripts/verify.sh
