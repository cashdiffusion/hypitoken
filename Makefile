SHELL := /bin/bash

WEB_DIR := internal/admin/web
WEB_DIST := $(WEB_DIR)/dist
GO_MAIN := ./cmd/server
BIN := bin/hypitoken

GOLANGCI := $(shell command -v golangci-lint 2>/dev/null || echo $(shell go env GOPATH)/bin/golangci-lint)

# Pin the Go toolchain to the one CI builds and releases with. go.mod's `go`
# line is only a MINIMUM, so a newer system Go silently wins over it — and
# newer is not automatically safe here. Go 1.27 moved HTTP/2 into the standard
# library, which switches golang.org/x/net/http2 into its delegating shim; that
# shim's newUserClientConn forgets to init the wrapped transport, so cc-core's
# uTLS path (which calls http2.Transport.NewClientConn directly, never
# RoundTrip) dereferences a nil *http.Transport and panics on the first OAuth
# token refresh. A go1.27 build of this repo passes every test and crash-loops
# in production. Bump this line only together with the CI go-version.
GOTOOLCHAIN ?= go1.25.6
export GOTOOLCHAIN

.PHONY: all build web web-install web-dev generate tidy clean help \
        lint lint-go lint-web fmt fmt-go fmt-web

all: build

help:
	@echo "Targets:"
	@echo "  make build        — build admin SPA and Go binary (default, Go $(GOTOOLCHAIN))"
	@echo "  make web          — build admin SPA only (bun run build)"
	@echo "  make web-dev      — run Vite dev server with API proxy to :8317"
	@echo "  make web-install  — install frontend deps"
	@echo "  make generate     — run go generate (invokes bun build)"
	@echo "  make tidy         — go mod tidy"
	@echo "  make lint         — run all linters (Go golangci-lint + web Biome)"
	@echo "  make lint-go      — golangci-lint over Go sources"
	@echo "  make lint-web     — Biome check over the admin SPA"
	@echo "  make fmt          — auto-format Go (golangci-lint fmt) + web (Biome)"
	@echo "  make clean        — remove dist, node_modules, bin"

web-install:
	cd $(WEB_DIR) && bun install

web: web-install
	cd $(WEB_DIR) && bun run build

web-dev:
	cd $(WEB_DIR) && bun run dev

build: web
	mkdir -p bin
	go build -o $(BIN) $(GO_MAIN)
	@go version -m $(BIN) | head -1

generate:
	go generate ./...

tidy:
	go mod tidy

# ---- lint & format ----

lint: lint-go lint-web

lint-go:
	$(GOLANGCI) run ./...

lint-web:
	cd $(WEB_DIR) && bun run lint

fmt: fmt-go fmt-web

fmt-go:
	$(GOLANGCI) fmt ./...

fmt-web:
	cd $(WEB_DIR) && bun run lint:fix

clean:
	rm -rf $(WEB_DIST)/* $(WEB_DIR)/node_modules bin/
	touch $(WEB_DIST)/.gitkeep
