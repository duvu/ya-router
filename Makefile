BINARY=github-copilot-svcs
VERSION ?= dev

all: build

build:
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) .

run: build
	./$(BINARY) run

auth:
	./$(BINARY) auth

models:
	./$(BINARY) models

config:
	./$(BINARY) config

clean:
	rm -f $(BINARY)

.PHONY: fmt vet tidy test help
fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

test:
	go test ./...

help:
	@echo "Targets: build run auth models config clean fmt vet tidy test"
