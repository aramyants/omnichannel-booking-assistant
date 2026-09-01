BINARY := gateway
IMAGE   := omnichannel-booking-assistant

.DEFAULT_GOAL := help
.PHONY: help fmt vet test test-race build run tidy check docker-build docker-run

help:
	@echo fmt           Format all Go source in place
	@echo vet           Report suspicious constructs
	@echo test          Run the test suite
	@echo test-race     Run the test suite with the race detector
	@echo build         Compile the gateway into bin/
	@echo run           Run the gateway from source
	@echo tidy          Sync go.mod and go.sum with the source
	@echo check         Run vet, test and build
	@echo docker-build  Build the container image
	@echo docker-run    Run the container image on port 8080

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./... -count=1

# The race detector needs cgo and a C compiler. CI runs this on Linux.
test-race:
	go test ./... -race -count=1

build:
	go build -trimpath -o bin/$(BINARY) ./cmd/gateway

run:
	go run ./cmd/gateway

tidy:
	go mod tidy

check: vet test build

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	docker run --rm -p 8080:8080 -e APP_ENV=development $(IMAGE)
