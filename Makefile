VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  ?= /usr/local

.PHONY: build test vet lint e2e install plugin clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/apple-compose ./cmd/apple-compose

test:
	go test ./...

vet:
	go vet ./...

e2e: build
	APPLE_COMPOSE_E2E=1 go test ./test/e2e/... -count=1 -v

install: build
	install -d $(PREFIX)/bin
	install -m 0755 bin/apple-compose $(PREFIX)/bin/apple-compose

plugin: build
	bin/apple-compose plugin install

clean:
	rm -rf bin dist
