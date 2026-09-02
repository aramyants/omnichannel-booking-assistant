BINARY  := gateway
IMAGE   := omnichannel-booking-assistant
VERSION ?= $(shell git rev-parse --short HEAD)

LDFLAGS := -s -w
ifneq ($(VERSION),)
LDFLAGS += -X main.version=$(VERSION)
endif

.DEFAULT_GOAL := help
.PHONY: help fmt vet lint test test-race emulator test-firestore build run tidy check vulncheck docker-build docker-run

help:
	@echo fmt           Format all Go source in place
	@echo vet           Report suspicious constructs
	@echo lint          Run golangci-lint
	@echo test          Run the test suite
	@echo test-race     Run the test suite with the race detector
	@echo emulator      Start the Firestore emulator for integration tests
	@echo test-firestore Run the Firestore tests against the emulator
	@echo vulncheck     Scan for known vulnerabilities
	@echo build         Compile the gateway into bin/
	@echo run           Run the gateway from source
	@echo tidy          Sync go.mod and go.sum with the source
	@echo check         Run vet, lint, test and build
	@echo docker-build  Build the container image
	@echo docker-run    Run the container image on port 8080

fmt:
	gofmt -w .

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run

test:
	go test ./... -count=1

# The race detector needs cgo and a C compiler. CI runs this on Linux.
test-race:
	go test ./... -race -count=1

# The Firestore tests are skipped unless the emulator is running, so an ordinary
# `make test` needs nothing installed.
emulator:
	docker compose up -d firestore

test-firestore:
	FIRESTORE_EMULATOR_HOST=localhost:8200 go test ./internal/adapters/persistence/firestore/... -count=1 -v

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/gateway

run:
	go run ./cmd/gateway

tidy:
	go mod tidy

check: vet lint test build

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

docker-run:
	docker run --rm -p 8080:8080 -e APP_ENV=development $(IMAGE):latest
