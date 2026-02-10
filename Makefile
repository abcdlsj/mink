BINARY=mink
PREFIX=$(HOME)/.local

.PHONY: all build install

all: build

build:
	go build -o $(BINARY) .

install: build
	mkdir -p $(PREFIX)/bin
	cp $(BINARY) $(PREFIX)/bin/
