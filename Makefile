SHELL := /bin/sh
.DEFAULT_GOAL := help
.DELETE_ON_ERROR:

GO ?= go
BIN ?= bin/actions-snitch
CMD_PATH ?= ./cmd/actions-snitch
PREFIX ?= $(HOME)/.local
TIMEOUT ?= 300s
PKGS ?= ./...
TEST_FLAGS ?=
ARGS ?= -h
GO_LDFLAGS ?= -s -w
TOOLS_BIN ?= $(CURDIR)/.tools/bin
GOSEC_GOTOOLCHAIN ?= go1.26.6
COVERAGE_FILE ?= coverage.out
COVERAGE_HTML ?= coverage.html
DIST_DIR ?= dist
VERSION ?= $(if $(RELEASE_TAG),$(RELEASE_TAG),$(shell git describe --tags --always --dirty 2>/dev/null || printf '%s' dev))
RELEASE_GOOS ?= $(shell $(GO) env GOOS)
RELEASE_GOARCH ?= $(shell $(GO) env GOARCH)
GOFILES = $(shell find cmd internal -type f -name '*.go' | LC_ALL=C sort)

GOIMPORTS ?= $(TOOLS_BIN)/goimports
STATICCHECK ?= $(TOOLS_BIN)/staticcheck
GOLANGCI_LINT ?= $(TOOLS_BIN)/golangci-lint
GOSEC ?= $(TOOLS_BIN)/gosec
GOVULNCHECK ?= $(TOOLS_BIN)/govulncheck
GOIMPORTS_PKG := golang.org/x/tools/cmd/goimports@v0.42.0
STATICCHECK_PKG := honnef.co/go/tools/cmd/staticcheck@v0.8.1
GOLANGCI_LINT_PKG := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
GOSEC_PKG := github.com/securego/gosec/v2/cmd/gosec@v2.24.6
GOVULNCHECK_PKG := golang.org/x/vuln/cmd/govulncheck@v1.3.0

# Git tag names are data, including when they contain shell metacharacters.
shquote = '$(subst ','"'"',$(1))'

COLOR ?= auto
COLOR_ENABLED :=
ifeq ($(COLOR),always)
COLOR_ENABLED := 1
else ifeq ($(COLOR),never)
COLOR_ENABLED :=
else ifneq ($(NO_COLOR),)
COLOR_ENABLED :=
else ifneq ($(MAKE_TERMOUT),)
COLOR_ENABLED := 1
endif
ifeq ($(COLOR_ENABLED),1)
COLOR_STEP := \033[1;36m
COLOR_OK := \033[1;32m
COLOR_TITLE := \033[1;37m
COLOR_TARGET := \033[36m
COLOR_RESET := \033[0m
endif
PRINT_STEP = @printf '$(COLOR_STEP)==>$(COLOR_RESET) %s\n' '$(1)'
PRINT_OK = @printf '$(COLOR_OK)OK:$(COLOR_RESET) %s\n' '$(1)'

.PHONY: help all check full-check fix qa qa-simple build run install release build-info
.PHONY: test test-short test-verbose race coverage coverage-html oracle
.PHONY: format format-check fmt-fix fmt-check imports-fix imports-check tidy-fix tidy-check
.PHONY: verify-modules lint vet staticcheck golangci-lint security gosec govulncheck tools clean

help: ## Show the available development commands.
	@printf '$(COLOR_TITLE)%s$(COLOR_RESET)\n\n' 'Actions Snitch Development Commands'
	@awk -v c='$(COLOR_TARGET)' -v r='$(COLOR_RESET)' 'BEGIN {FS = ":.*## "}; /^[A-Za-z0-9_.-]+:.*## / {printf "%s%-20s%s %s\n", c, $$1, r, $$2}' $(MAKEFILE_LIST) | LC_ALL=C sort
	@printf '\n%s\n' 'Examples: make run ARGS="-o json"; make test PKGS=./internal/git; make release VERSION=v1.0.0'

all: ## Apply mechanical fixes, then run the standard validation gate.
	@$(MAKE) --no-print-directory fix
	@$(MAKE) --no-print-directory check

check: ## Run formatting, modules, static analysis, tests, race, gosec, and build.
	@$(MAKE) --no-print-directory verify-modules format-check tidy-check
	@$(MAKE) --no-print-directory lint test race gosec build
	$(call PRINT_OK,The standard validation gate passed.)

full-check: ## Run the standard gate, live vulnerability checks, and the Bash oracle.
	@$(MAKE) --no-print-directory check
	@$(MAKE) --no-print-directory govulncheck oracle

fix: ## Apply source formatting, import formatting, and module tidying.
	@$(MAKE) --no-print-directory format
	@$(MAKE) --no-print-directory tidy-fix

qa: format-check tidy-check lint ## Run static quality checks without tests or a build.

qa-simple: ## Format sources and run tests.
	@$(MAKE) --no-print-directory format
	@$(MAKE) --no-print-directory test

build: ## Build the local executable used by the root compatibility symlink.
	$(call PRINT_STEP,Building the executable.)
	@$(GO) build -trimpath -ldflags $(call shquote,$(GO_LDFLAGS)) -o "$(BIN)" "$(CMD_PATH)"
	$(call PRINT_OK,The executable was built.)

run: build ## Run the local executable; ARGS defaults to -h.
	@"$(abspath $(BIN))" $(ARGS)

build-info: build ## Display the binary's embedded Go module and VCS information.
	@$(GO) version -m "$(BIN)"

install: build ## Install into PREFIX/bin, honoring DESTDIR for staged installs.
	$(call PRINT_STEP,Installing the executable.)
	@install -d "$(DESTDIR)$(PREFIX)/bin"
	@install -m 755 "$(BIN)" "$(DESTDIR)$(PREFIX)/bin/actions-snitch"
	$(call PRINT_OK,The executable was installed.)

release: ## Build a versioned archive and SHA-256 checksum for one OS/architecture.
	$(call PRINT_STEP,Building a release archive.)
	@set -eu; \
		version=$(call shquote,$(VERSION)); target_os=$(call shquote,$(RELEASE_GOOS)); target_arch=$(call shquote,$(RELEASE_GOARCH)); \
		case "$$version" in ''|*[!A-Za-z0-9._+-]*) printf '%s\n' 'VERSION must contain only letters, digits, dots, underscores, pluses, or hyphens.' >&2; exit 1;; esac; \
		case "$$target_os:$$target_arch" in *[!a-z0-9:]*|:*|*:) printf '%s\n' 'The release OS and architecture must be nonempty lowercase platform names.' >&2; exit 1;; esac; \
		if command -v sha256sum >/dev/null 2>&1; then checksum=sha256sum; \
		elif command -v shasum >/dev/null 2>&1; then checksum=shasum; \
		else printf '%s\n' 'Release checksums require sha256sum or shasum.' >&2; exit 1; fi; \
		zip_tool=; if [ "$$target_os" = windows ]; then \
			if command -v zip >/dev/null 2>&1; then zip_tool=zip; \
			elif command -v bsdtar >/dev/null 2>&1; then zip_tool=bsdtar; \
			else printf '%s\n' 'Windows release archives require zip or bsdtar.' >&2; exit 1; fi; \
		fi; \
		mkdir -p "$(DIST_DIR)"; \
		tmp=$$(mktemp -d "$(DIST_DIR)/.actions-snitch-release.XXXXXXXX"); \
		trap 'rm -rf "$$tmp"' EXIT HUP INT TERM; \
		base="actions-snitch_$${version}_$${target_os}_$${target_arch}"; \
		mkdir "$$tmp/$$base"; executable=actions-snitch; \
		if [ "$$target_os" = windows ]; then executable=actions-snitch.exe; fi; \
		GOOS="$$target_os" GOARCH="$$target_arch" CGO_ENABLED=0 $(GO) build -trimpath -ldflags $(call shquote,$(GO_LDFLAGS)) -o "$$tmp/$$base/$$executable" "$(CMD_PATH)"; \
		cp README.md "$$tmp/$$base/"; \
		if [ -f LICENSE ]; then cp LICENSE "$$tmp/$$base/"; fi; \
		printf '%s\n' "$$version" > "$$tmp/$$base/VERSION"; \
		if [ "$$target_os" = windows ]; then extension=zip; \
			if [ "$$zip_tool" = zip ]; then (cd "$$tmp" && zip -qr "$$base.zip" "$$base"); \
			else bsdtar --format zip -cf "$$tmp/$$base.zip" -C "$$tmp" "$$base"; fi; \
		else extension=tar.gz; tar -C "$$tmp" -czf "$$tmp/$$base.tar.gz" "$$base"; fi; \
		(cd "$$tmp" && if [ "$$checksum" = sha256sum ]; then sha256sum "$$base.$$extension"; else shasum -a 256 "$$base.$$extension"; fi) > "$$tmp/$$base.$$extension.sha256"; \
		mv "$$tmp/$$base.$$extension" "$$tmp/$$base.$$extension.sha256" "$(DIST_DIR)/"; \
		printf '$(COLOR_OK)OK:$(COLOR_RESET) %s\n' "$(DIST_DIR)/$$base.$$extension" "$(DIST_DIR)/$$base.$$extension.sha256"

test: ## Run tests; PKGS, TIMEOUT, and TEST_FLAGS can narrow the run.
	$(call PRINT_STEP,Running tests.)
	@$(GO) test -timeout "$(TIMEOUT)" $(TEST_FLAGS) $(PKGS)
	$(call PRINT_OK,The tests passed.)

test-short: ## Run tests with Go's short-mode flag.
	@$(GO) test -short -timeout "$(TIMEOUT)" $(TEST_FLAGS) $(PKGS)

test-verbose: ## Run tests with verbose output.
	@$(GO) test -v -timeout "$(TIMEOUT)" $(TEST_FLAGS) $(PKGS)

race: ## Run the race detector with the configured package selection and timeout.
	$(call PRINT_STEP,Running the race detector.)
	@$(GO) test -race -timeout "$(TIMEOUT)" $(TEST_FLAGS) $(PKGS)
	$(call PRINT_OK,The race detector passed.)

coverage: ## Run tests with atomic coverage and print function coverage.
	$(call PRINT_STEP,Collecting test coverage.)
	@$(GO) test -covermode=atomic -coverprofile="$(COVERAGE_FILE)" -timeout "$(TIMEOUT)" $(TEST_FLAGS) $(PKGS)
	@$(GO) tool cover -func="$(COVERAGE_FILE)"

coverage-html: coverage ## Generate a standalone HTML coverage report.
	@$(GO) tool cover -html="$(COVERAGE_FILE)" -o "$(COVERAGE_HTML)"
	$(call PRINT_OK,The HTML coverage report was generated.)

oracle: ## Run the original Bash integration suite against its frozen fixture.
	$(call PRINT_STEP,Running the Bash oracle.)
	@bash .github/scripts/lint-test

format: ## Apply gofmt and goimports in sequence.
	@$(MAKE) --no-print-directory fmt-fix
	@$(MAKE) --no-print-directory imports-fix

format-check: fmt-check imports-check ## Check source formatting and imports without editing files.

fmt-fix: ## Apply gofmt to Go source files.
	@gofmt -w $(GOFILES)

fmt-check: ## Check gofmt without editing files.
	@set -eu; issues=$$(gofmt -l $(GOFILES)); if [ -n "$$issues" ]; then printf '%s\n' "$$issues" 'Run make fmt-fix.' >&2; exit 1; fi

imports-fix: $(GOIMPORTS) ## Apply goimports to Go source files.
	@"$(GOIMPORTS)" -w $(GOFILES)

imports-check: $(GOIMPORTS) ## Check import formatting without editing files.
	@set -eu; issues=$$("$(GOIMPORTS)" -l $(GOFILES)); if [ -n "$$issues" ]; then printf '%s\n' "$$issues" 'Run make imports-fix.' >&2; exit 1; fi

tidy-fix: ## Apply go mod tidy.
	@$(GO) mod tidy

tidy-check: ## Check module tidiness without editing go.mod or go.sum.
	@$(GO) mod tidy -diff

verify-modules: ## Download and verify the pinned module dependencies.
	$(call PRINT_STEP,Verifying modules.)
	@$(GO) mod download
	@$(GO) mod verify

lint: vet staticcheck golangci-lint ## Run the configured static-analysis tools.

vet: ## Run go vet.
	$(call PRINT_STEP,Running go vet.)
	@$(GO) vet $(PKGS)

staticcheck: $(STATICCHECK) ## Run staticcheck.
	$(call PRINT_STEP,Running staticcheck.)
	@"$(STATICCHECK)" $(PKGS)

golangci-lint: $(GOLANGCI_LINT) ## Run the repository's golangci-lint configuration.
	$(call PRINT_STEP,Running golangci-lint.)
	@"$(GOLANGCI_LINT)" run

security: gosec govulncheck ## Run source-security analysis and live vulnerability checks.

gosec: $(GOSEC) ## Run gosec with its compatible Go toolchain.
	$(call PRINT_STEP,Running gosec.)
	@GOTOOLCHAIN="$(GOSEC_GOTOOLCHAIN)" "$(GOSEC)" -quiet ./...

govulncheck: $(GOVULNCHECK) ## Check reachable vulnerabilities using the public Go vulnerability database.
	$(call PRINT_STEP,Running govulncheck.)
	@"$(GOVULNCHECK)" -test ./...

tools: $(GOIMPORTS) $(STATICCHECK) $(GOLANGCI_LINT) $(GOSEC) $(GOVULNCHECK) ## Install pinned analysis tools into TOOLS_BIN.

$(TOOLS_BIN):
	@mkdir -p "$@"

$(TOOLS_BIN)/goimports: Makefile | $(TOOLS_BIN)
	@GOBIN="$(TOOLS_BIN)" $(GO) install $(GOIMPORTS_PKG)

$(TOOLS_BIN)/staticcheck: Makefile | $(TOOLS_BIN)
	@GOBIN="$(TOOLS_BIN)" $(GO) install $(STATICCHECK_PKG)

$(TOOLS_BIN)/golangci-lint: Makefile | $(TOOLS_BIN)
	@GOBIN="$(TOOLS_BIN)" $(GO) install $(GOLANGCI_LINT_PKG)

$(TOOLS_BIN)/gosec: Makefile | $(TOOLS_BIN)
	@GOBIN="$(TOOLS_BIN)" $(GO) install $(GOSEC_PKG)

$(TOOLS_BIN)/govulncheck: Makefile | $(TOOLS_BIN)
	@GOBIN="$(TOOLS_BIN)" $(GO) install $(GOVULNCHECK_PKG)

clean: ## Remove the selected binary and coverage files while retaining tools and archives.
	$(call PRINT_STEP,Removing local build outputs.)
	@set -eu; for artifact in "$(BIN)" "$(COVERAGE_FILE)" "$(COVERAGE_HTML)"; do \
		if [ -L "$$artifact" ] || git ls-files --error-unmatch -- "$$artifact" >/dev/null 2>&1; then \
			printf 'Refusing to remove a symlink or tracked file: %s\n' "$$artifact" >&2; exit 1; \
		fi; \
	done; \
	rm -f -- "$(BIN)" "$(COVERAGE_FILE)" "$(COVERAGE_HTML)"
	$(call PRINT_OK,Local build outputs were removed.)
