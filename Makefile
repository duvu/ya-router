BINARY ?= ya-router
VERSION ?= dev

all: build

build:
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) ./src

run: build
	./$(BINARY) run

auth: build
	./$(BINARY) auth

models: build
	./$(BINARY) models

config: build
	./$(BINARY) config

clean:
	rm -f $(BINARY)

fmt:
	gofmt -w ./src

fmt-check:
	@test -z "$$(gofmt -l ./src)" || (gofmt -l ./src && exit 1)

vet:
	go vet ./src/...

test:
	go test -race -count=1 ./src/...

check: fmt-check vet test build

help:
	@echo "Targets: build run auth models config clean fmt fmt-check vet test check"

.PHONY: all build run auth models config clean fmt fmt-check vet test check help
