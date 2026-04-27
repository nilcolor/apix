BRANCH    := $(shell git symbolic-ref --short HEAD 2>/dev/null || echo "dev")
HASH      := $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo "dev")
TIMESTAMP := $(shell date -u +'%Y%m%d_%H%M%S')
REVISION  := $(BRANCH)-$(HASH)-$(TIMESTAMP)

.PHONY: help build test race vet lint fmt all
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

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format source with gofmt
	gofmt -w .

all: test build ## Run tests then build
