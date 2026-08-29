.DEFAULT_GOAL := all

.PHONY: all help build test benchmark coverage format check lint lint-ci mulint \
	check-secrets check-packages clean docker docker-smoke deploy dev dev-config watch vendor web web-dev

GO ?= go
BUILD_DIR ?= build
BINARY ?= $(BUILD_DIR)/cue
DOCKER_TAG ?= cue:dev
GOLANGCI_LINT ?= golangci-lint

# Pinned rather than @latest. A new linter release adds checks, and with
# @latest that turns a green build red with nobody having changed any code.
# Raise it deliberately, and fix what the new version finds in that commit.
GOLANGCI_LINT_VERSION ?= v2.13.1

GOPACKAGES ?= . ./cmd/... ./internal/... ./tools/...

VERSION ?= $(shell git describe --tags 2>/dev/null || echo 0.1.0)
COMMIT ?= $(shell git describe --match=NeVeRmAtCh --always --abbrev=40 --dirty)
LDFLAGS := -s -w -extldflags "-static" \
	-X github.com/ziyan/cue/internal/version.version=$(VERSION) \
	-X github.com/ziyan/cue/internal/version.commit=$(COMMIT)

DIST_DIR ?= dist

GOFMTARGS := $(shell find . -mindepth 1 -maxdepth 1 -type d -not -path ./vendor -not -path ./.git) \
	$(shell find . -mindepth 1 -maxdepth 1 -type f -iname '*.go')

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "; print "Targets:"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: format build test ## Format, build and test

build: ## Build the cue binary
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -mod=vendor -ldflags '$(LDFLAGS)' -o $(BINARY) .

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

# Not part of lint-ci: it fetches Debian's package indexes, and a linter that
# needs the network fails for reasons that have nothing to do with the change.
# CI runs it as a step of its own.
check-packages: ## Fail if the image asks for a package an architecture does not have
	@$(GO) run -mod=vendor ./tools/checkpackages

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

web: ## Build the management interface into internal/web/dist
	@cd web && npm ci --no-audit --no-fund && npm run build

web-dev: ## Run the interface against a device (CUE=http://host:8080)
	@cd web && npm run dev

docker: ## Build the container image
	@docker build -t $(DOCKER_TAG) -f Dockerfile \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) .

docker-smoke: docker ## Run the whole daemon in the image against a virtual screen and prove it works
	@$(GO) run -mod=vendor ./tools/smoke -image $(DOCKER_TAG)

# Some tests need a program the image has and a development machine does not —
# certutil and an X server, for two. On a development machine those tests skip,
# and a skip proves nothing: the point of them is that the *image* has what the
# code needs. So the test binaries are built and run inside the image itself,
# where nothing skips.
#
# Built with the race detector, because the tests that only run here are the
# ones that open X connections, and the bug that took a display down in the
# field was two goroutines opening one at the same moment. Without -race that
# test passes on a good day.
docker-test: docker ## Run the tests inside the image, where the programs they need exist
	@mkdir -p build/tests
	@for package in $(IMAGE_TESTED_PACKAGES); do \
		name=$$(basename $$package); \
		CGO_ENABLED=1 $(GO) test -mod=vendor -race -c -o build/tests/$$name.test ./$$package || exit 1; \
		echo "==> $$package, inside $(DOCKER_TAG)"; \
		docker run --rm -e TMPDIR=/tmp \
			-v $(PWD)/build/tests/$$name.test:/$$name.test:ro \
			--entrypoint /$$name.test $(DOCKER_TAG) -test.v > build/tests/$$name.out 2>&1; \
		status=$$?; \
		grep -E "^(---|ok|FAIL|PASS)" build/tests/$$name.out || true; \
		if [ $$status -ne 0 ]; then cat build/tests/$$name.out; exit 1; fi; \
	done

# The packages whose tests depend on something only the image has.
IMAGE_TESTED_PACKAGES = internal/browser internal/network internal/display internal/web internal/vncserver

deploy: docker ## Send this build to a machine and start it (HOST=... [WAIT=2h] [DISPLAY_MANAGER=stop] [CONFIG=...] [DOCKER_SOCKET=yes])
	@$(GO) run -mod=vendor ./tools/deploy -host $(HOST) -image $(DOCKER_TAG) \
		$(if $(WAIT),-wait $(WAIT),) \
		$(if $(CONFIG),-config $(CONFIG),) \
		$(if $(filter stop,$(DISPLAY_MANAGER)),-stop-display-manager,) \
		$(if $(filter yes,$(DOCKER_SOCKET)),-docker-socket,)

watch: ## Rebuild on source change (requires inotifywait)
	@set -e; \
	while true; do \
		inotifywait --quiet --recursive --event modify --event delete --event move \
			--exclude '(^\./(\.git|vendor|build|dist)/)' .; \
		$(MAKE) format build || true; \
	done

clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR) $(DIST_DIR)
