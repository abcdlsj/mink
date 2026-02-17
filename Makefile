BINARY=mink
PREFIX=$(HOME)/.local
GOBIN=$(shell go env GOPATH)/bin
PLIST_SRC=deploy/com.mink.agent.plist.tpl
PLIST_DST=$(HOME)/Library/LaunchAgents/com.mink.agent.plist
MINK_LOG_DIR=$(HOME)/.mink

.PHONY: all build install launchd-install launchd-uninstall

all: build

build:
	go build -o $(BINARY) ./cmd/mink

install:
	go install ./cmd/mink/

launchd-install: install
	@mkdir -p $(MINK_LOG_DIR)
	@sed -e 's|MINK_BIN|$(GOBIN)/mink|g' -e 's|MINK_LOG_DIR|$(MINK_LOG_DIR)|g' $(PLIST_SRC) > $(PLIST_DST)
	launchctl unload $(PLIST_DST) 2>/dev/null || true
	launchctl load $(PLIST_DST)
	@echo "mink daemon installed and started"

launchd-uninstall:
	launchctl unload $(PLIST_DST) 2>/dev/null || true
	rm -f $(PLIST_DST)
	@echo "mink daemon uninstalled"
