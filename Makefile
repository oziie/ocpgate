BINARY   := ocpgate
PKG      := github.com/oziie/ocpgate
CMD      := ./cmd/ocpgate
DIST     := dist

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/pkg/version.Version=$(VERSION) \
	-X $(PKG)/pkg/version.Commit=$(COMMIT) \
	-X $(PKG)/pkg/version.BuildDate=$(BUILD_DATE)

.PHONY: all
all: check build

.PHONY: build
build: ## Build the binary into dist/
	@mkdir -p $(DIST)
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) $(CMD)

.PHONY: install
install: ## Install the binary into GOBIN
	go install -ldflags "$(LDFLAGS)" $(CMD)

.PHONY: run
run: ## Build and run
	go run -ldflags "$(LDFLAGS)" $(CMD)

.PHONY: test
test: ## Run all tests
	go test ./...

.PHONY: cover
cover: ## Run tests and open the coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

.PHONY: test-report
test-report: ## Write a durable pass/fail + coverage report to reports/
	@./scripts/test-report.sh

.PHONY: fmt
fmt: ## Format all Go sources
	gofmt -w .

.PHONY: check
check: ## Verify formatting, vet, and tests
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "run 'make fmt'" && exit 1)
	go vet ./...
	go test ./...

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(DIST) coverage.out

.PHONY: help
help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
