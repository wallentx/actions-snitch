GO ?= go
PREFIX ?= $(HOME)/.local
GOSEC_GOTOOLCHAIN ?= go1.26.6
TOOLS_BIN := $(CURDIR)/.tools/bin
GOFILES := $(shell find cmd internal -name '*.go')

.PHONY: build install test race vet format format-check lint security check tools oracle clean

build:
	$(GO) build -trimpath -o bin/actions-snitch ./cmd/actions-snitch

install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 755 bin/actions-snitch "$(DESTDIR)$(PREFIX)/bin/actions-snitch"

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

format: $(TOOLS_BIN)/goimports
	gofmt -w $(GOFILES)
	$(TOOLS_BIN)/goimports -w $(GOFILES)

format-check: $(TOOLS_BIN)/goimports
	@test -z "$$(gofmt -l $(GOFILES))" || { gofmt -l $(GOFILES); exit 1; }
	@test -z "$$($(TOOLS_BIN)/goimports -l $(GOFILES))" || { $(TOOLS_BIN)/goimports -l $(GOFILES); exit 1; }

lint: vet $(TOOLS_BIN)/staticcheck $(TOOLS_BIN)/golangci-lint
	$(TOOLS_BIN)/staticcheck ./...
	$(TOOLS_BIN)/golangci-lint run

security: $(TOOLS_BIN)/gosec
	GOTOOLCHAIN=$(GOSEC_GOTOOLCHAIN) $(TOOLS_BIN)/gosec -quiet ./...

check: format-check
	$(GO) mod verify
	$(GO) mod tidy -diff
	$(MAKE) lint test race security build

oracle:
	bash .github/scripts/lint-test

tools: $(TOOLS_BIN)/goimports $(TOOLS_BIN)/staticcheck $(TOOLS_BIN)/golangci-lint $(TOOLS_BIN)/gosec

$(TOOLS_BIN)/goimports: Makefile
	GOBIN=$(TOOLS_BIN) $(GO) install golang.org/x/tools/cmd/goimports@v0.42.0

$(TOOLS_BIN)/staticcheck: Makefile
	GOBIN=$(TOOLS_BIN) $(GO) install honnef.co/go/tools/cmd/staticcheck@v0.8.1

$(TOOLS_BIN)/golangci-lint: Makefile
	GOBIN=$(TOOLS_BIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

$(TOOLS_BIN)/gosec: Makefile
	GOBIN=$(TOOLS_BIN) $(GO) install github.com/securego/gosec/v2/cmd/gosec@v2.24.6

clean:
	$(GO) clean ./...
