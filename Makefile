BINARY=mink

VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILDTIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S' 2>/dev/null || echo "unknown")

LDFLAGS=-ldflags "-X github.com/abcdlsj/mink.Version=$(VERSION) \
                  -X github.com/abcdlsj/mink.Commit=$(COMMIT) \
                  -X github.com/abcdlsj/mink.BuildTime=$(BUILDTIME)"

.PHONY: all build install version

all: build

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/mink

install:
	go install $(LDFLAGS) ./cmd/mink/

version:
	@echo "version: $(VERSION)"
	@echo "commit:  $(COMMIT)"
	@echo "built:   $(BUILDTIME)"
