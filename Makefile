.DEFAULT_GOAL := build

GO ?= go
CGO_ENABLED ?= 0
export CGO_ENABLED

.PHONY: all fmt fmt-check vet test race check build run gui package resources version-check clean

all: check build

fmt:
	$(GO) run ./scripts/tasks.go fmt

fmt-check:
	$(GO) run ./scripts/tasks.go fmt-check

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

race:
	$(GO) run ./scripts/tasks.go race

version-check:
	$(GO) run ./scripts/tasks.go version-check

check: fmt-check version-check vet test

build:
	$(GO) run ./scripts/tasks.go build

run:
	$(GO) run ./cmd/badmeta --help

gui:
	$(GO) run ./cmd/badmeta-gui

package:
	$(GO) run ./scripts/tasks.go package

resources:
	$(GO) run ./scripts/tasks.go resources

clean:
	$(GO) run ./scripts/tasks.go clean
