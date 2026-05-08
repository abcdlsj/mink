GO ?= go
PKG ?= ./cmd/sumi
BIN_DIR ?= bin
GOEXE ?= $(shell $(GO) env GOEXE)
GOBIN ?= $(shell $(GO) env GOBIN)
GOPATH ?= $(shell $(GO) env GOPATH)
BIN ?= $(BIN_DIR)/sumi$(GOEXE)
MAIN ?= main

ifeq ($(strip $(GOBIN)),)
INSTALL_DIR ?= $(firstword $(subst :, ,$(GOPATH)))/bin
else
INSTALL_DIR ?= $(GOBIN)
endif

INSTALL_BIN ?= $(INSTALL_DIR)/sumi$(GOEXE)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -X '$(MAIN).Version=$(VERSION)' -X '$(MAIN).Commit=$(COMMIT)' -X '$(MAIN).BuildTime=$(BUILD_TIME)'

.PHONY: help build install version clean

help:
	@printf "%s\n" \
		"make build    Build ./bin/sumi with version metadata" \
		"make install  Build and overwrite $(INSTALL_BIN)" \
		"make version  Print build metadata values" \
		"make clean    Remove local build artifacts"

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

install:
	@mkdir -p $(INSTALL_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(INSTALL_BIN) $(PKG)

version:
	@printf "version: %s\ncommit: %s\nbuilt: %s\n" "$(VERSION)" "$(COMMIT)" "$(BUILD_TIME)"

clean:
	rm -rf $(BIN_DIR)
