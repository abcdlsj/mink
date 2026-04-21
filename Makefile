GO ?= go
PKG ?= ./cmd/mink
BIN_DIR ?= bin
BIN ?= $(BIN_DIR)/mink
MAIN ?= main

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -X '$(MAIN).Version=$(VERSION)' -X '$(MAIN).Commit=$(COMMIT)' -X '$(MAIN).BuildTime=$(BUILD_TIME)'

.PHONY: help build install version clean

help:
	@printf "%s\n" \
		"make build    Build ./bin/mink with version metadata" \
		"make install  Install mink with version metadata" \
		"make version  Print build metadata values" \
		"make clean    Remove local build artifacts"

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

install:
	$(GO) install -ldflags "$(LDFLAGS)" $(PKG)

version:
	@printf "version: %s\ncommit: %s\nbuilt: %s\n" "$(VERSION)" "$(COMMIT)" "$(BUILD_TIME)"

clean:
	rm -rf $(BIN_DIR)
