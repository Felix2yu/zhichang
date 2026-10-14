GO      ?= go
PNPM    ?= pnpm
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all web check build test cover vet migrate seed doctor run mcp docker clean

all: web build

web:
	cd web && $(PNPM) install --frozen-lockfile && $(PNPM) build

# 带类型的项目要把类型检查并入构建：build 绿但 svelte-check 有问题没人知道
check:
	cd web && $(PNPM) run check

build:
	# cmd/zhichangd 的 //go:embed all:build 要求 web/build 非空；
	# adapter-static 每次构建都会清空它，这里先补一个占位文件
	@mkdir -p web/build && [ -f web/build/.gitkeep ] || touch web/build/.gitkeep
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/zhichangd ./cmd/zhichangd

test:
	$(GO) test ./...

# 覆盖率按全仓语句口径汇总（-coverpkg 让 api/mcp 的测试也算进仓储层的覆盖）。
# 集成测试会在测试进程里自举一个 PostgreSQL，第一次跑要下载发行包（约 30MB，之后离线可跑）。
cover:
	$(GO) test ./internal/... -count=1 -covermode=atomic \
		-coverpkg=$(shell go list ./internal/... | grep -v testdb | paste -sd, -) \
		-coverprofile=coverage.out
	$(GO) tool cover -func=coverage.out | tail -1

vet:
	$(GO) vet ./...

migrate:
	$(GO) run ./cmd/zhichangd migrate

seed:
	$(GO) run ./cmd/zhichangd seed-metrics

doctor:
	$(GO) run ./cmd/zhichangd doctor

run: build
	./bin/zhichangd

mcp: build
	./bin/zhichangd mcp

# 装配式镜像：必须先有 bin/zhichangd 与已构建的前端（前端在 go build 时内嵌）
docker: web build
	docker build -t zhichang:$(VERSION) .

clean:
	rm -rf bin web/build web/.svelte-kit
