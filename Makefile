# Github App for managing Github repos.

## Project Variables

PROJECT_NAME  := repo-guardian
PROJECT_OWNER := donaldgifford
DESCRIPTION   := Github app for managing Github repos
PROJECT_URL   := https://github.com/$(PROJECT_OWNER)/$(PROJECT_NAME)

## Go Variables

GO          ?= go
GO_PACKAGE  := github.com/$(PROJECT_OWNER)/$(PROJECT_NAME)
GOOS        ?= $(shell $(GO) env GOOS)
GOARCH      ?= $(shell $(GO) env GOARCH)

GOIMPORTS_LOCAL_ARG := -local github.com/donaldgifford

## Build Directories

BUILD_DIR      := build
BIN_DIR        := $(BUILD_DIR)/bin

## Version Information

COMMIT_HASH ?= $(shell git rev-parse --short HEAD 2>/dev/null)
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
CUR_VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || git describe --tags 2>/dev/null || echo "v0.0.0-$(COMMIT_HASH)")

## Build Variables

COVERAGE_OUT := coverage.out



###############
##@ Go Development

.PHONY: build
.PHONY: test test-all test-coverage
.PHONY: lint lint-fix fmt clean
.PHONY: run run-local test-api ci check
.PHONY: release-check release-local

## Build Targets

build: build-core ## Build everything (core)

build-core: ## Build core binary
	@ $(MAKE) --no-print-directory log-$@
	@mkdir -p $(BIN_DIR)
	@go build -o $(BIN_DIR)/$(PROJECT_NAME) ./cmd/$(PROJECT_NAME)
	@echo "✓ Core binaries built"

## Testing

test: ## Run all tests with race detector
	@ $(MAKE) --no-print-directory log-$@
	@go test -v -race ./...

test-all: test ## Run all tests (core + plugins)

test-pkg: ## Test specific package (usage: make test-pkg PKG=./pkg/api)
	@ $(MAKE) --no-print-directory log-$@
	@go test -v -race $(PKG)

test-report: ## Run tests with coverage report then open
	@ $(MAKE) --no-print-directory log-$@
	@go test -coverprofile=$(COVERAGE_OUT) ./...
	@go tool cover -html=$(COVERAGE_OUT)

test-coverage: ## Run tests with coverage report
	@ $(MAKE) --no-print-directory log-$@
	@go test -v -race -coverprofile=$(COVERAGE_OUT) ./...


## Code Quality

lint: ## Run golangci-lint
	@ $(MAKE) --no-print-directory log-$@
	@golangci-lint run ./...

lint-fix: ## Run golangci-lint with auto-fix
	@ $(MAKE) --no-print-directory log-$@
	@golangci-lint run --fix ./...

fmt: ## Format code with gofmt and goimports
	@ $(MAKE) --no-print-directory log-$@
	@gofmt -s -w .
	@goimports -w $(GOIMPORTS_LOCAL_ARG) .
	@goimports -w $(GOIMPORTS_LOCAL_ARG)  cmd/ pkg/

clean: ## Remove build artifacts
	@ $(MAKE) --no-print-directory log-$@
	@rm -rf $(BIN_DIR)/
	@rm -f $(COVERAGE_OUT)
	@go clean -cache
	@find . -name "*.test" -delete
	@echo "✓ Build artifacts cleaned"

## Application Services

run: ## Run CLI command
	@ $(MAKE) --no-print-directory log-$@
	./build/bin/repo-guardian

run-local: build ## Run exporter with local config
	@ $(MAKE) --no-print-directory log-$@
	@$(BIN_DIR)/$(PROJECT_NAME)

## CI/CD

ci: lint test build ## Run CI pipeline (lint + test + build)
	@ $(MAKE) --no-print-directory log-$@
	@echo "✓ CI pipeline complete"

check: lint test ## Quick pre-commit check (lint + test)
	@ $(MAKE) --no-print-directory log-$@
	@echo "✓ Pre-commit checks passed"

# =============================================================================
# Release Targets
# =============================================================================

release: ## Create release (use with TAG=v1.0.0)
	@ $(MAKE) --no-print-directory log-$@
	@if [ -z "$(TAG)" ]; then \
		echo "Error: TAG is required. Usage: make release TAG=v1.0.0"; \
		exit 1; \
	fi
	git tag -a $(TAG) -m "Release $(TAG)"
	git push origin $(TAG)

release-check:
	@ $(MAKE) --no-print-directory log-$@
	goreleaser check


release-local: ## Test goreleaser without publishing
	@ $(MAKE) --no-print-directory log-$@
	goreleaser release --snapshot --clean --skip=publish --skip=sign


########################################################################
## Self-Documenting Makefile Help                                     ##
## https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html ##
########################################################################

########
##@ Help

.PHONY: help
help:   ## Display this help
	@awk -v "col=\033[36m" -v "nocol=\033[0m" ' \
		BEGIN { FS = ":.*##" ; printf "Usage:\n  make %s<target>%s\n\n", col, nocol } \
		/^[a-zA-Z_0-9-]+:.*?##/ { printf "  %s%-25s%s %s\n", col, $$1, nocol, $$2 } \
		/^##@/ { printf "\n%s%s%s\n", nocol, substr($$0, 5), nocol } \
	' $(MAKEFILE_LIST)

## Log Pattern
## Automatically logs what a target does by extracting its ## comment
log-%:
	@grep -h -E '^$*:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN { FS = ":.*?## " }; { printf "\033[36m==> %s\033[0m\n", $$2 }'
