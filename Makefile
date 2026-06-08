GO ?= go
NPM ?= npm
WAILS ?= wails
RSVG_CONVERT ?= rsvg-convert
SIPS ?= sips
ICONUTIL ?= iconutil
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
APP_ICON_ICNS ?= $(DESKTOP_DIR)/build/darwin/icons.icns
APP_ICONSET ?= $(DESKTOP_DIR)/build/darwin/Sumi.iconset
DESKTOP_APP ?= $(DESKTOP_DIR)/build/bin/sumi.app
APP_INSTALL_DIR ?= /Applications
INSTALL_APP ?= $(APP_INSTALL_DIR)/Sumi.app

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

.PHONY: help build install install-cli install-desktop version clean frontend-deps frontend desktop desktop-icons desktop-app

help:
	@printf "%-22s %s\n" \
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

frontend-deps:
	cd $(FRONTEND_DIR) && $(NPM) install

frontend: frontend-deps
	cd $(FRONTEND_DIR) && $(NPM) run build

desktop: frontend build

desktop-icons:
	@mkdir -p $(dir $(APP_ICON_PNG)) $(dir $(APP_ICON_ICNS)) $(APP_ICONSET)
	$(RSVG_CONVERT) -w 1024 -h 1024 $(ICON_SRC) -o $(APP_ICON_PNG)
	$(SIPS) -z 16 16 $(APP_ICON_PNG) --out $(APP_ICONSET)/icon_16x16.png >/dev/null
	$(SIPS) -z 32 32 $(APP_ICON_PNG) --out $(APP_ICONSET)/icon_16x16@2x.png >/dev/null
	$(SIPS) -z 32 32 $(APP_ICON_PNG) --out $(APP_ICONSET)/icon_32x32.png >/dev/null
	$(SIPS) -z 64 64 $(APP_ICON_PNG) --out $(APP_ICONSET)/icon_32x32@2x.png >/dev/null
	$(SIPS) -z 128 128 $(APP_ICON_PNG) --out $(APP_ICONSET)/icon_128x128.png >/dev/null
	$(SIPS) -z 256 256 $(APP_ICON_PNG) --out $(APP_ICONSET)/icon_128x128@2x.png >/dev/null
	$(SIPS) -z 256 256 $(APP_ICON_PNG) --out $(APP_ICONSET)/icon_256x256.png >/dev/null
	$(SIPS) -z 512 512 $(APP_ICON_PNG) --out $(APP_ICONSET)/icon_256x256@2x.png >/dev/null
	$(SIPS) -z 512 512 $(APP_ICON_PNG) --out $(APP_ICONSET)/icon_512x512.png >/dev/null
	cp $(APP_ICON_PNG) $(APP_ICONSET)/icon_512x512@2x.png
	$(ICONUTIL) -c icns $(APP_ICONSET) -o $(APP_ICON_ICNS)
	@rm -rf $(APP_ICONSET)

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
	rm -rf $(APP_ICONSET)
