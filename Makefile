MODULE   = $(shell $(GO) list -m)
DATE    ?= $(shell date +%FT%T%z)
VERSION ?= $(shell git describe --tags --always --dirty --match=v* 2> /dev/null || \
			cat .version 2> /dev/null || echo v0)
PKG 	 =
PKGS     = $(or $(PKG),$(shell $(GO) list ./...))
BINARY   = bambulan

GO      = go
TIMEOUT = 15
V = 0
Q = $(if $(filter 1,$V),,@)
M = $(shell if [ "$$(tput colors 2> /dev/null || echo 0)" -ge 8 ]; then printf "\033[34;1m▶\033[0m"; else printf "▶"; fi)

GENERATED = # List of generated files

GOIMPORTS = $(shell which goimports)
GOTESTSUM=$(shell which gotestsum)
GOLANGREVIVE=$(shell which revive)

.PHONY: all build run clean test build-all fuzz

all: fmt revive-run $(GENERATED) build ; $(info $(M) building executable…) @ ## Build program binary

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/bambulan

fuzz:
	go test -fuzz=FuzzPacketMatchesFilter -fuzztime=10s ./pkg/repeater

# Cross-compilation targets
build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY)-linux-amd64 ./cmd/bambulan

build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY)-darwin-amd64 ./cmd/bambulan

build-windows-amd64:
	GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY)-windows-amd64.exe ./cmd/bambulan

build-all: build-linux-amd64 build-darwin-amd64 build-windows-amd64

# Tools

goimports:
ifeq (, $(GOIMPORTS))
	$(error "No goimport in $$PATH, please run 'make install-tools')
endif

gotestsum:
ifeq (, $(GOTESTSUM))
	$(error "No gotestsum in $$PATH, please run 'make install-tools')
endif

revive:
ifeq (, $(GOLANGREVIVE))
	$(error "No revive $$PATH, please run 'make install-tools')
endif

update-tools:
	go install golang.org/x/tools/cmd/goimports@latest
	go install gotest.tools/gotestsum@latest
	go install github.com/mgechev/revive@latest

install-tools:
	test -x "$(GOIMPORTS)" || go install golang.org/x/tools/cmd/goimports@latest
	test -x "$(GOTESTSUM)" || go install gotest.tools/gotestsum@latest
	test -x "$(GOLANGREVIVE)" || go install github.com/mgechev/revive@latest

# Generate

# Tests

TEST_TARGETS := test-short test-race
.PHONY: $(TEST_TARGETS) check test tests
test-short:   ARGS=-short        ## Run only short tests
test-race:    ARGS=-race         ## Run tests with race detector
$(TEST_TARGETS): NAME=$(MAKECMDGOALS:test-%=%)
$(TEST_TARGETS): test
check test tests: fmt revive-run $(GENERATED) | gotestsum ; $(info $(M) running $(NAME:%=% )tests…) @ ## Run tests
	$Q mkdir -p test
	$Q gotestsum --junitfile test/tests.xml -- -timeout $(TIMEOUT)s $(ARGS) $(PKGS)
.PHONY: test-bench
test-bench: $(GENERATED) ; $(info $(M) running benchmarks…) @ ## Run benchmarks
	$Q gotestsum -f standard-quiet -- --timeout $(TIMEOUT)s -run=__absolutelynothing__ -bench=. $(PKGS)

COVERAGE_MODE = atomic
.PHONY: test-coverage
test-coverage: fmt revive-run $(GENERATED)
test-coverage: | gotestsum ; $(info $(M) running coverage tests…) @ ## Run coverage tests
	$Q mkdir -p test
	$Q gotestsum -- \
		-coverpkg=$(shell echo $(PKGS) | tr ' ' ',') \
		-covermode=$(COVERAGE_MODE) \
		-coverprofile=test/profile.out $(PKGS)
	$Q $(GO) tool cover -html=test/profile.out -o test/coverage.html
	$Q $(GO) tool cover -func=test/profile.out > test/coverage.txt

.PHONY: revive-run
revive-run: | revive ; $(info $(M) running revive…) @
	$Q revive

.PHONY: fmt
fmt: | goimports ; $(info $(M) running gofmt…) @ ## Run gofmt on all source files
	$Q goimports -local $(MODULE) -w $(shell $(GO) list -f '{{$$d := .Dir}}{{range $$f := .GoFiles}}{{printf "%s/%s\n" $$d $$f}}{{end}}{{range $$f := .CgoFiles}}{{printf "%s/%s\n" $$d $$f}}{{end}}{{range $$f := .TestGoFiles}}{{printf "%s/%s\n" $$d $$f}}{{end}}' $(PKGS))

# Misc

.PHONY: clean
clean: ; $(info $(M) cleaning…)	@ ## Cleanup everything
	@rm -rf $(PKG) test $(GENERATED) $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-darwin-amd64 $(BINARY)-windows-amd64.exe

.PHONY: help
help:
	@grep -hE '^[ a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-17s\033[0m %s\n", $$1, $$2}'

.PHONY: version
version:
	@echo $(VERSION)
