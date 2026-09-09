VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  ?= /usr/local

.PHONY: build test cover vet lint e2e install plugin clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/apple-compose ./cmd/apple-compose
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/apple-docker ./cmd/apple-docker

test:
	go test ./...

# Unit coverage across internal packages; opens nothing, prints the per
# package totals and writes coverage.out for `go tool cover -html`.
cover:
	go test ./internal/... -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out | tail -1

vet:
	go vet ./...

e2e: build
	APPLE_COMPOSE_E2E=1 go test ./test/e2e/... -count=1 -v

install: build
	install -d $(PREFIX)/bin
	install -m 0755 bin/apple-compose $(PREFIX)/bin/apple-compose
	install -m 0755 bin/apple-docker $(PREFIX)/bin/apple-docker

plugin: build
	bin/apple-compose plugin install

clean:
	rm -rf bin dist coverage.out
