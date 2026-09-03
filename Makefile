BRANCH    := $(shell git symbolic-ref --short HEAD 2>/dev/null || echo "dev")
HASH      := $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo "dev")
TIMESTAMP := $(shell date -u +'%Y%m%d_%H%M%S')
REVISION  := $(BRANCH)-$(HASH)-$(TIMESTAMP)

# CI reads the same file, so a local `make lint` runs the exact linter CI runs.
GOLANGCI_VERSION := $(shell cat .golangci-version)
GOLANGCI         := $(shell go env GOPATH)/bin/golangci-lint

.PHONY: help build test race vet lint lint-tools fmt all
.DEFAULT_GOAL: help

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[33m%-16s\033[0m %s\n", $$1, $$2}'

build: ## Build binary to bin/apix with revision baked in
	go build -ldflags="-X main.revision=$(REVISION)" -o bin/apix ./cmd/apix

test: ## Run tests
	go test -timeout=60s ./...

race: ## Run tests with race detector
	go test -race -timeout=60s ./...

vet: ## Run go vet
	go vet ./...

lint: lint-tools ## Run golangci-lint (pinned to .golangci-version, same as CI)
	$(GOLANGCI) run

lint-tools: ## Install the pinned golangci-lint if missing or the wrong version
	@if ! [ -x "$(GOLANGCI)" ] || ! $(GOLANGCI) --version 2>/dev/null | grep -q "$(patsubst v%,%,$(GOLANGCI_VERSION))"; then \
		echo "installing golangci-lint $(GOLANGCI_VERSION)"; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION); \
	fi

fmt: ## Format source with gofmt
	gofmt -w .

all: test build ## Run tests then build
