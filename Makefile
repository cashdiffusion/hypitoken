SHELL := /bin/bash

WEB_DIR := internal/admin/web
WEB_DIST := $(WEB_DIR)/dist
GO_MAIN := ./cmd/server
BIN := bin/hypitoken

.PHONY: all build web web-install web-dev generate tidy clean help

all: build

help:
	@echo "Targets:"
	@echo "  make build        — build admin SPA and Go binary (default)"
	@echo "  make web          — build admin SPA only (bun run build)"
	@echo "  make web-dev      — run Vite dev server with API proxy to :8317"
	@echo "  make web-install  — install frontend deps"
	@echo "  make generate     — run go generate (invokes bun build)"
	@echo "  make tidy         — go mod tidy"
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

generate:
	go generate ./...

tidy:
	go mod tidy

clean:
	rm -rf $(WEB_DIST)/* $(WEB_DIR)/node_modules bin/
	touch $(WEB_DIST)/.gitkeep
