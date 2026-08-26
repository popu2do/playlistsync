# ==============================================================================
# playlistsync Makefile
# ==============================================================================

BINARY_NAME   := playlistsync
# Windows local builds get the .exe suffix (E2E harness and CI users expect
# bin/playlistsync.exe on win32; build-all uses explicit matrix names).
EXE_SUFFIX    :=
ifeq ($(OS),Windows_NT)
EXE_SUFFIX    := .exe
endif
CMD_DIR       := ./cmd/playlistsync
BUILD_DIR     := ./bin
DIST_DIR      := ./dist
VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT        ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME    ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || echo "unknown")

# Go build flags: strip debug symbols (-s -w), trimpath for privacy
LDFLAGS       := -s -w \
                 -X 'playlistsync/internal/config.Version=$(VERSION)' \
                 -X 'playlistsync/internal/config.GitCommit=$(COMMIT)' \
                 -X 'playlistsync/internal/config.BuildTime=$(BUILD_TIME)'

GO_BUILD_CMD  := go build -trimpath -ldflags="$(LDFLAGS)"

.PHONY: all build web-build build-all clean test test-race test-e2e test-coverage lint vet fmt tidy run help

all: fmt vet test build ## 默认目标：格式化、静态检查、测试并编译本地二进制

help: ## 显示 Makefile 帮助信息
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

tidy: ## 整理 Go 依赖
	go mod tidy

fmt: ## 格式化所有 Go 源码
	go fmt ./...

vet: ## 运行 Go 官方静态分析检查
	go vet ./...

lint: ## 静态检查 (go vet + staticcheck)；staticcheck 缺失时自动安装
	go vet ./...
	command -v staticcheck >/dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@latest
	staticcheck ./...

test: ## 运行所有 Go 单元/集成测试 + 前端 Vitest
	go test ./... -count=1
	cd web && pnpm run test

test-race: ## CI 竞态门禁：CGO_ENABLED=1 go test -race ./...
	CGO_ENABLED=1 go test -race ./... -count=1

test-e2e: build ## 构建单二进制后运行 Playwright E2E (6 条 TC)
	cd web && pnpm exec playwright test

test-coverage: ## 运行测试并生成代码覆盖率报告
	@mkdir -p $(BUILD_DIR)
	go test -coverprofile=$(BUILD_DIR)/coverage.out ./...
	go tool cover -html=$(BUILD_DIR)/coverage.out -o $(BUILD_DIR)/coverage.html
	@echo "Coverage report written to $(BUILD_DIR)/coverage.html"

web-build: ## 构建 web SPA 到 internal/web/static/dist (embed.FS 源，必须先行)
	cd web && pnpm run build

build: web-build ## 先构建 SPA (embed.FS 源)，再构建本地二进制
	@test -d internal/web/static/dist || (echo "ERROR: internal/web/static/dist missing — run 'make web-build' first"; exit 1)
	@mkdir -p $(BUILD_DIR)
	$(GO_BUILD_CMD) -o $(BUILD_DIR)/$(BINARY_NAME)$(EXE_SUFFIX) $(CMD_DIR)
	@echo "Build successful: $(BUILD_DIR)/$(BINARY_NAME)$(EXE_SUFFIX)"

build-all: clean ## 交叉编译跨平台二进制包 (Windows, macOS, Linux)
	@mkdir -p $(DIST_DIR)
	# Windows amd64 & arm64
	GOOS=windows GOARCH=amd64 $(GO_BUILD_CMD) -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe $(CMD_DIR)
	GOOS=windows GOARCH=arm64 $(GO_BUILD_CMD) -o $(DIST_DIR)/$(BINARY_NAME)-windows-arm64.exe $(CMD_DIR)
	# Darwin (macOS) Intel & Apple Silicon
	GOOS=darwin GOARCH=amd64 $(GO_BUILD_CMD) -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 $(CMD_DIR)
	GOOS=darwin GOARCH=arm64 $(GO_BUILD_CMD) -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 $(CMD_DIR)
	# Linux amd64 & arm64
	GOOS=linux GOARCH=amd64 $(GO_BUILD_CMD) -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 $(CMD_DIR)
	GOOS=linux GOARCH=arm64 $(GO_BUILD_CMD) -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 $(CMD_DIR)
	@echo "All release binaries generated in $(DIST_DIR)/"

clean: ## 清理构建产物与临时测试文件
	rm -rf $(BUILD_DIR) $(DIST_DIR) coverage.out coverage.html