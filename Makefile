BINARY_NAME := gsc
BUILD_DIR := build
GO := go

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

GOLANGCI_LINT_VERSION ?= v2.12.2
GOFUMPT_VERSION ?= v0.11.0
GOVULNCHECK_VERSION ?= v1.5.0

.DEFAULT_GOAL := help

.PHONY: build
build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) .

.PHONY: test
test:
	$(GO) test ./...

.PHONY: test-race
test-race:
	$(GO) test -race ./...

.PHONY: docs
docs:
	$(GO) run ./scripts/generate-command-docs

.PHONY: docs-check
docs-check:
	$(GO) run ./scripts/generate-command-docs --check

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: format
format:
	$(GO) fmt ./...
	@if command -v gofumpt >/dev/null 2>&1; then gofumpt -w .; fi

.PHONY: format-check
format-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Files not formatted with gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	@if command -v gofumpt >/dev/null 2>&1; then \
		unformatted="$$(gofumpt -l .)"; \
		if [ -n "$$unformatted" ]; then \
			echo "Files not formatted with gofumpt:"; \
			echo "$$unformatted"; \
			exit 1; \
		fi; \
	fi

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: govulncheck
govulncheck:
	govulncheck ./...

.PHONY: tools
tools:
	$(GO) install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

.PHONY: check
check: format-check docs-check vet lint test test-race

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)

.PHONY: help
help:
	@echo "Galaxy Store CLI development targets"
	@echo ""
	@echo "  build         Build $(BUILD_DIR)/$(BINARY_NAME)"
	@echo "  test          Run all tests"
	@echo "  test-race     Run all tests with the race detector"
	@echo "  docs          Generate the command reference"
	@echo "  docs-check    Check that the command reference is current"
	@echo "  vet           Run go vet"
	@echo "  format        Format Go source"
	@echo "  format-check  Check Go formatting without changing files"
	@echo "  lint          Run golangci-lint"
	@echo "  govulncheck   Scan reachable Go code for known vulnerabilities"
	@echo "  tools         Install pinned development tools"
	@echo "  check         Run local CI-equivalent checks"
	@echo "  clean         Remove build output"
