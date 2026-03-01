BINARY=mink
PREFIX=$(HOME)/.local
GOBIN=$(shell go env GOPATH)/bin
PLIST_SRC=deploy/com.mink.agent.plist.tpl
PLIST_DST=$(HOME)/Library/LaunchAgents/com.mink.agent.plist
MINK_LOG_DIR=$(HOME)/.mink

# Version info from git
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILDTIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S' 2>/dev/null || echo "unknown")

LDFLAGS=-ldflags "-X github.com/abcdlsj/mink.Version=$(VERSION) \
                  -X github.com/abcdlsj/mink.Commit=$(COMMIT) \
                  -X github.com/abcdlsj/mink.BuildTime=$(BUILDTIME)"

.PHONY: all build install launchd-install launchd-uninstall version

all: build

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/mink

install:
	go install $(LDFLAGS) ./cmd/mink/

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

version:
	@echo "version: $(VERSION)"
	@echo "commit:  $(COMMIT)"
	@echo "built:   $(BUILDTIME)"
