SHELL := /usr/bin/env bash

BINARY := route-controller
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT ?= $(shell git rev-parse --verify HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/zijiren233/route-controller/internal/version.Version=$(VERSION) \
	-X github.com/zijiren233/route-controller/internal/version.GitCommit=$(GIT_COMMIT) \
	-X github.com/zijiren233/route-controller/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all build build-linux test integration-test lint lint-fix vet verify clean

all: verify build

build:
	mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/route-controller

build-linux:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags '$(LDFLAGS)' -o bin/$(BINARY)-linux-amd64 ./cmd/route-controller

test:
	go test -race -coverprofile=coverage.out ./...

integration-test:
	go test -count=1 -tags=integration ./test/integration/...

lint:
	golangci-lint run

lint-fix:
	golangci-lint run --fix

vet:
	go vet ./...

verify: test vet lint

clean:
	rm -rf -- bin coverage.out
