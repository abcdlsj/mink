BINARY=mink
GOBIN=$(shell go env GOPATH)/bin
PLIST_SRC=deploy/com.mink.agent.plist.tpl
PLIST_DST=$(HOME)/Library/LaunchAgents/com.mink.agent.plist
SYSTEMD_SRC=deploy/mink.service.tpl
SYSTEMD_DST=$(HOME)/.config/systemd/user/mink.service
MINK_HOME=$(HOME)/.mink
MINK_LOG_DIR=$(MINK_HOME)
MINK_WORKDIR=$(CURDIR)
DAEMON_SCRIPT=./scripts/install-mink.sh

VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILDTIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S' 2>/dev/null || echo "unknown")

LDFLAGS=-ldflags "-X github.com/abcdlsj/mink.Version=$(VERSION) \
                  -X github.com/abcdlsj/mink.Commit=$(COMMIT) \
                  -X github.com/abcdlsj/mink.BuildTime=$(BUILDTIME)"

.PHONY: all build install version \
	daemon-install daemon-uninstall daemon-start daemon-stop daemon-restart daemon-status daemon-reload daemon-devbuild daemon-upgrade daemon-update \
	launchd-install launchd-uninstall

all: build

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/mink

install:
	go install $(LDFLAGS) ./cmd/mink/


daemon-install:
	MINK_BIN=$(GOBIN)/mink MINK_HOME=$(MINK_HOME) MINK_LOG_DIR=$(MINK_LOG_DIR) MINK_WORKDIR=$(MINK_WORKDIR) $(DAEMON_SCRIPT) install


daemon-uninstall:
	MINK_BIN=$(GOBIN)/mink MINK_HOME=$(MINK_HOME) MINK_LOG_DIR=$(MINK_LOG_DIR) MINK_WORKDIR=$(MINK_WORKDIR) $(DAEMON_SCRIPT) uninstall


daemon-start:
	MINK_BIN=$(GOBIN)/mink MINK_HOME=$(MINK_HOME) MINK_LOG_DIR=$(MINK_LOG_DIR) MINK_WORKDIR=$(MINK_WORKDIR) $(DAEMON_SCRIPT) start


daemon-stop:
	MINK_BIN=$(GOBIN)/mink MINK_HOME=$(MINK_HOME) MINK_LOG_DIR=$(MINK_LOG_DIR) MINK_WORKDIR=$(MINK_WORKDIR) $(DAEMON_SCRIPT) stop


daemon-restart:
	MINK_BIN=$(GOBIN)/mink MINK_HOME=$(MINK_HOME) MINK_LOG_DIR=$(MINK_LOG_DIR) MINK_WORKDIR=$(MINK_WORKDIR) $(DAEMON_SCRIPT) restart


daemon-status:
	MINK_BIN=$(GOBIN)/mink MINK_HOME=$(MINK_HOME) MINK_LOG_DIR=$(MINK_LOG_DIR) MINK_WORKDIR=$(MINK_WORKDIR) $(DAEMON_SCRIPT) status


daemon-reload:
	MINK_BIN=$(GOBIN)/mink MINK_HOME=$(MINK_HOME) MINK_LOG_DIR=$(MINK_LOG_DIR) MINK_WORKDIR=$(MINK_WORKDIR) $(DAEMON_SCRIPT) reload


daemon-devbuild:
	MINK_BIN=$(GOBIN)/mink MINK_HOME=$(MINK_HOME) MINK_LOG_DIR=$(MINK_LOG_DIR) MINK_WORKDIR=$(MINK_WORKDIR) $(DAEMON_SCRIPT) devbuild


daemon-upgrade:
	MINK_BIN=$(GOBIN)/mink MINK_HOME=$(MINK_HOME) MINK_LOG_DIR=$(MINK_LOG_DIR) MINK_WORKDIR=$(MINK_WORKDIR) $(DAEMON_SCRIPT) upgrade


daemon-update: daemon-upgrade

launchd-install: daemon-install

launchd-uninstall: daemon-uninstall

version:
	@echo "version: $(VERSION)"
	@echo "commit:  $(COMMIT)"
	@echo "built:   $(BUILDTIME)"
