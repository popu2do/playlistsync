# ==============================================================================
# playlistsync Makefile
# ==============================================================================

BINARY_NAME   := playlistsync
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

.PHONY: all build build-all clean test test-race test-coverage lint vet fmt tidy run help

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

test: ## 运行所有单元测试与集成测试
	go test -v ./...

test-race: ## 开启竞态检测运行测试
	go test -race -v ./...

test-coverage: ## 运行测试并生成代码覆盖率报告
	@mkdir -p $(BUILD_DIR)
	go test -coverprofile=$(BUILD_DIR)/coverage.out ./...
	go tool cover -html=$(BUILD_DIR)/coverage.out -o $(BUILD_DIR)/coverage.html
	@echo "Coverage report written to $(BUILD_DIR)/coverage.html"

build: tidy ## 构建当前平台的本地二进制文件
	@mkdir -p $(BUILD_DIR)
	$(GO_BUILD_CMD) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "Build successful: $(BUILD_DIR)/$(BINARY_NAME)"

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