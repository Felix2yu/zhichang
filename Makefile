GO      ?= go
PNPM    ?= pnpm
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all web build test vet migrate doctor seed run mcp docker clean

all: web build

web:
	cd web && $(PNPM) install --frozen-lockfile && $(PNPM) build

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/zhichangd ./cmd/zhichangd
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/zhichangctl ./cmd/zhichangctl
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/zhichang-mcp ./cmd/zhichang-mcp

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

migrate:
	$(GO) run ./cmd/zhichangctl migrate

seed:
	$(GO) run ./cmd/zhichangctl seed-metrics

doctor:
	$(GO) run ./cmd/zhichangctl doctor

run: build
	./bin/zhichangd

mcp: build
	./bin/zhichang-mcp

docker:
	docker build -t zhichang:$(VERSION) .

clean:
	rm -rf bin web/build web/.svelte-kit
