.DEFAULT_GOAL := all

.PHONY: all help build web test benchmark coverage format check lint lint-ci mulint \
	check-secrets clean docker docker-smoke dev dev-config watch vendor

GO ?= go
NPM ?= npm
WEB_DIR ?= web
BUILD_DIR ?= build
BINARY ?= $(BUILD_DIR)/cue
DOCKER_TAG ?= cue:dev
GOLANGCI_LINT ?= golangci-lint

# Pinned rather than @latest. A new linter release adds checks, and with
# @latest that turns a green build red with nobody having changed any code.
# Raise it deliberately, and fix what the new version finds in that commit.
GOLANGCI_LINT_VERSION ?= v2.13.1

# Explicit package list: ./... would also pick up stray Go files vendored
# inside web/node_modules by npm packages.
GOPACKAGES ?= . ./cmd/... ./internal/... ./tools/...

VERSION ?= $(shell git describe --tags 2>/dev/null || echo 0.1.0)
COMMIT ?= $(shell git describe --match=NeVeRmAtCh --always --abbrev=40 --dirty)
LDFLAGS := -s -w -extldflags "-static" \
	-X github.com/ziyan/cue/internal/version.version=$(VERSION) \
	-X github.com/ziyan/cue/internal/version.commit=$(COMMIT)

GOFMTARGS := $(shell find . -mindepth 1 -maxdepth 1 -type d -not -path ./vendor -not -path ./web -not -path ./.git) \
	$(shell find . -mindepth 1 -maxdepth 1 -type f -iname '*.go')

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "; print "Targets:"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: format build test ## Format, build and test

build: ## Build the cue binary
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -mod=vendor -ldflags '$(LDFLAGS)' -o $(BINARY) .

web: $(WEB_DIR)/node_modules ## Build the web interface into internal/web/static
	cd $(WEB_DIR) && $(NPM) run build

$(WEB_DIR)/node_modules: $(WEB_DIR)/package.json $(WEB_DIR)/package-lock.json
	cd $(WEB_DIR) && $(NPM) ci --no-audit --no-fund
	@touch $@

vendor: ## Refresh the vendored dependencies
	$(GO) mod tidy
	$(GO) mod vendor

format: ## Format Go code
	@gofmt -l -w $(GOFMTARGS)

check: ## Fail if code is not formatted
	@if [ -n "$$(gofmt -l -e $(GOFMTARGS))" ]; then \
		gofmt -l -e -d $(GOFMTARGS); \
		echo "ERROR: run 'make format' before committing" >&2; \
		exit 1; \
	fi

lint: lint-ci mulint ## Run every linter (what to run before committing)

check-secrets: ## Fail if a credential or a private address is in a tracked file
	@$(GO) run -mod=vendor ./tools/checksecrets

lint-ci: check-secrets ## Run the linters CI runs
	@set -e; \
	if ! hash $(GOLANGCI_LINT) >/dev/null 2>&1; then \
		$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	fi; \
	$(GOLANGCI_LINT) run $(GOPACKAGES)

# mulint enforces this project's naming conventions. It is a local-only
# check: CI does not run it, so a contributor without mulint on their PATH is
# never blocked by it.
mulint: ## Run the local naming checks
	@if hash mulint >/dev/null 2>&1; then \
		mulint $(GOPACKAGES); \
	else \
		echo "mulint is not installed; skipping the naming checks."; \
	fi

test: ## Run the tests
	@mkdir -p $(BUILD_DIR)
	$(GO) test -mod=vendor -cover -coverprofile=$(BUILD_DIR)/coverage.out $(GOPACKAGES)
	@$(GO) tool cover -func=$(BUILD_DIR)/coverage.out | tail -1

benchmark: ## Run the benchmarks
	@$(GO) test -mod=vendor -bench=. -run='^$$' $(GOPACKAGES)

coverage: test ## Generate an HTML coverage report
	@$(GO) tool cover -html=$(BUILD_DIR)/coverage.out -o $(BUILD_DIR)/coverage.html

dev-config: build ## Create dev/cue.yaml if it is not there yet
	@mkdir -p dev
	@test -f dev/cue.yaml || ./$(BINARY) config init --output dev/cue.yaml --development

dev: dev-config ## Run the daemon locally against a virtual screen
	@./$(BINARY) run --config dev/cue.yaml

docker: ## Build the container image
	@docker build -t $(DOCKER_TAG) -f deploy/Dockerfile \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) .

docker-smoke: docker ## Run the whole daemon in the image against a virtual screen and prove it works
	@$(GO) run -mod=vendor ./tools/smoke -image $(DOCKER_TAG)

watch: ## Rebuild on source change (requires inotifywait)
	@set -e; \
	while true; do \
		inotifywait --quiet --recursive --event modify --event delete --event move \
			--exclude '(^\./(\.git|vendor|build|web/node_modules)/)' .; \
		$(MAKE) format build || true; \
	done

clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR) $(WEB_DIR)/node_modules $(WEB_DIR)/dist
	rm -rf internal/web/static/*
	@touch internal/web/static/.gitkeep
