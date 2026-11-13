.PHONY: all full help fmt fix lint revive-run vuln build \
	build-linux-amd64 build-darwin-amd64 build-windows-amd64 build-all \
	test test-short test-race coverage coverage-html test-coverage \
	benchmark bench test-bench install-tools update-tools version clean

GO       ?= go
BINARY   := bambulan
CMD      := ./cmd/bambulan
PKGS     := ./...
COVERPKG := ./...
FMT_DIRS := .
DATE     ?= $(shell date +%FT%T%z)
VERSION  ?= $(shell git describe --tags --always --dirty --match=v* 2>/dev/null || cat .version 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION) -X main.buildDate=$(DATE)
BENCH    ?= .

# Verbosity:
#   V=0 (default): one status line per operation; command output shown only on error.
#   V=1:           status lines + standard tool output (e.g. package test summaries).
#   V=2:           echoed commands + verbose flags (-v).
VERBOSE  ?= 0
V        ?= $(VERBOSE)
ifeq ($(V),0)
Q        := @
RUN      := @quiet() { out=$$("$$@" 2>&1) || { printf '%s\n' "$$out" >&2; exit 1; }; }; quiet
TEST_V   :=
VULN_V   :=
else ifeq ($(V),1)
Q        := @
RUN      := @
TEST_V   :=
VULN_V   :=
else
Q        :=
RUN      :=
TEST_V   := -v
VULN_V   := -show verbose
endif

# Tool discovery (PATH -> GOPATH/bin -> ~/go/bin)
GOBIN       := $(shell $(GO) env GOPATH)/bin
find_tool    = $(or $(shell command -v $(1) 2>/dev/null),$(wildcard $(GOBIN)/$(1)),$(wildcard $(HOME)/go/bin/$(1)))
REVIVE      := $(call find_tool,revive)
STATICCHECK := $(call find_tool,staticcheck)
GOVULNCHECK := $(call find_tool,govulncheck)

define require_tool
	@if [ -z "$(1)" ] || [ ! -x "$(1)" ]; then \
		echo "Error: $(2) not found (run 'make install-tools')" >&2; \
		exit 1; \
	fi
endef

all: fix fmt lint build test ## Run fix, fmt, lint, build, and test

full: clean fix fmt lint vuln build test coverage ## Clean and run all checks including vuln and coverage

help: ## Show available targets
	@echo "Usage: make [target] [V=0|1|2]"
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

fmt: ## Format source files with gofmt
	@echo "Formatting..."
	$(RUN) gofmt -w $(FMT_DIRS)

fix: ## Modernize Go code with go fix
	@echo "Fixing..."
	$(RUN) $(GO) fix $(PKGS)

lint: ## Run go vet, revive, and staticcheck
	$(call require_tool,$(REVIVE),revive)
	$(call require_tool,$(STATICCHECK),staticcheck)
	@echo "Linting..."
	$(RUN) $(GO) vet $(PKGS)
	$(RUN) "$(REVIVE)" -set_exit_status $(PKGS)
	$(RUN) "$(STATICCHECK)" $(PKGS)

revive-run: lint

vuln: ## Check for vulnerabilities with govulncheck
	$(call require_tool,$(GOVULNCHECK),govulncheck)
	@echo "Checking vulnerabilities..."
	$(RUN) "$(GOVULNCHECK)" $(VULN_V) $(PKGS)

build: ## Build the project
	@echo "Building..."
	$(RUN) $(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

build-linux-amd64: ## Cross-compile for Linux (amd64)
	@echo "Building $(BINARY)-linux-amd64..."
	$(RUN) env GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-amd64 $(CMD)

build-darwin-amd64: ## Cross-compile for macOS (amd64)
	@echo "Building $(BINARY)-darwin-amd64..."
	$(RUN) env GOOS=darwin GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY)-darwin-amd64 $(CMD)

build-windows-amd64: ## Cross-compile for Windows (amd64)
	@echo "Building $(BINARY)-windows-amd64.exe..."
	$(RUN) env GOOS=windows GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY)-windows-amd64.exe $(CMD)

build-all: build-linux-amd64 build-darwin-amd64 build-windows-amd64 ## Cross-compile for Linux, macOS, and Windows

test: ## Run unit tests with race detector
	@echo "Running tests..."
	$(RUN) $(GO) test -race $(TEST_V) $(PKGS)

test-short: ## Run short unit tests with race detector
	@echo "Running short tests..."
	$(RUN) $(GO) test -short -race $(TEST_V) $(PKGS)

test-race: test

coverage: ## Run tests with coverage summary (coverage.out)
	@echo "Generating coverage..."
	$(RUN) $(GO) test -coverprofile=coverage.out -covermode=atomic -coverpkg=$(COVERPKG) $(PKGS)
	@$(GO) tool cover -func=coverage.out | $(if $(filter 0,$(V)),tail -n 1,cat)

coverage-html: coverage ## Generate HTML coverage report (coverage.html)
	@echo "Generating coverage HTML..."
	@$(GO) tool cover -html=coverage.out -o coverage.html

test-coverage: coverage-html

benchmark: ## Run benchmarks
	@echo "Running benchmarks..."
	$(Q) $(GO) test -bench=$(BENCH) -benchmem -run='^$$' $(TEST_V) $(PKGS)

bench: benchmark
test-bench: benchmark

install-tools: ## Install revive, staticcheck, and govulncheck
	@echo "Installing tools..."
	$(RUN) $(GO) install github.com/mgechev/revive@latest
	$(RUN) $(GO) install honnef.co/go/tools/cmd/staticcheck@latest
	$(RUN) $(GO) install golang.org/x/vuln/cmd/govulncheck@latest

update-tools: install-tools

version: ## Show current version
	@echo "$(VERSION)"

clean: ## Remove build and test artifacts
	@echo "Cleaning..."
	$Q rm -rf $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-darwin-amd64 $(BINARY)-windows-amd64.exe test coverage.out coverage.html coverage.txt *.test
