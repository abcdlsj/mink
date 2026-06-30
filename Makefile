GO ?= go
NPM ?= npm
WAILS ?= wails
RSVG_CONVERT ?= rsvg-convert
PKG ?= ./cmd/sumi
BIN_DIR ?= bin
GOEXE ?= $(shell $(GO) env GOEXE)
GOBIN ?= $(shell $(GO) env GOBIN)
GOPATH ?= $(shell $(GO) env GOPATH)
BIN ?= $(BIN_DIR)/sumi$(GOEXE)
MAIN ?= main
FRONTEND_DIR ?= plugins/desktop/frontend
DESKTOP_DIR ?= cmd/sumi-desktop
ICON_SRC ?= $(FRONTEND_DIR)/public/sumi-icon.svg
APP_ICON_PNG ?= $(DESKTOP_DIR)/build/appicon.png
DESKTOP_APP ?= $(DESKTOP_DIR)/build/bin/sumi.app
APP_INSTALL_DIR ?= /Applications
INSTALL_APP ?= $(APP_INSTALL_DIR)/Sumi.app
RUN_ADDR ?= 127.0.0.1:7799

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

.PHONY: help run build install install-cli install-desktop version clean frontend-deps frontend desktop desktop-icons desktop-app

help:
	@printf "%-22s %s\n" \
		"make run" "Run the browser desktop UI on $(RUN_ADDR)" \
		"make build" "Build ./bin/sumi with version metadata" \
		"make install" "Build and install CLI plus macOS app" \
		"make install-cli" "Build and install $(INSTALL_BIN)" \
		"make install-desktop" "Build and install /Applications/Sumi.app" \
		"make frontend" "Build the desktop frontend" \
		"make desktop" "Build frontend then ./bin/sumi" \
		"make desktop-icons" "Generate macOS app icons from $(ICON_SRC)" \
		"make desktop-app" "Build the macOS Sumi.app via Wails" \
		"make version" "Print build metadata values" \
		"make clean" "Remove local build artifacts"

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

run: frontend
	$(GO) run $(PKG) desktop -addr $(RUN_ADDR)

frontend-deps:
	cd $(FRONTEND_DIR) && $(NPM) install

frontend: frontend-deps
	cd $(FRONTEND_DIR) && $(NPM) run build

desktop: frontend build

desktop-icons:
	@mkdir -p $(dir $(APP_ICON_PNG))
	$(RSVG_CONVERT) -w 1024 -h 1024 $(ICON_SRC) -o $(APP_ICON_PNG)

desktop-app: frontend desktop-icons
	cd $(DESKTOP_DIR) && $(WAILS) build -skipbindings -s

install: install-cli install-desktop

install-cli: desktop
	@mkdir -p $(INSTALL_DIR)
	install -m 0755 $(BIN) $(INSTALL_BIN)

install-desktop: desktop-app
	@mkdir -p $(APP_INSTALL_DIR)
	@rm -rf $(INSTALL_APP)
	ditto $(DESKTOP_APP) $(INSTALL_APP)

version:
	@printf "version: %s\ncommit: %s\nbuilt: %s\n" "$(VERSION)" "$(COMMIT)" "$(BUILD_TIME)"

clean:
	rm -rf $(BIN_DIR)
	rm -rf $(DESKTOP_DIR)/build/bin
