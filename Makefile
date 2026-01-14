# Makefile for VCP (Video Codec Pipeline)

.PHONY: all build build-linux build-all clean test install uninstall redis-start redis-stop help

# 变量定义
BINARY_NAME := vcp
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

# Go 参数
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# 默认目标
all: build

# 本地构建
build:
	@echo "==> Building $(BINARY_NAME)..."
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY_NAME) .
	@echo "==> Build complete: ./$(BINARY_NAME)"

# 构建 Linux 版本（用于服务器部署）
build-linux:
	@echo "==> Building $(BINARY_NAME) for Linux..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_NAME)-linux-amd64 .
	@echo "==> Build complete: ./$(BINARY_NAME)-linux-amd64"

# Linux ARM64 构建
build-linux-arm64:
	@echo "==> Building $(BINARY_NAME) for Linux ARM64..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_NAME)-linux-arm64 .
	@echo "==> Build complete: ./$(BINARY_NAME)-linux-arm64"

# 构建所有平台
build-all: build build-linux build-linux-arm64
	@echo "==> All platform builds complete"

# 清理
clean:
	@echo "==> Cleaning..."
	rm -f $(BINARY_NAME) $(BINARY_NAME)-linux-amd64 $(BINARY_NAME)-linux-arm64
	go clean -cache

# 运行（开发用）
run: build
	./$(BINARY_NAME)

# 安装到系统
install: build-linux
	sudo ./install.sh install

# 卸载
uninstall:
	sudo ./install.sh uninstall

# 检查依赖
check:
	./install.sh check

# 启动本地 Redis（开发用）
redis-start:
	@echo "==> Starting Redis in Docker..."
	docker run -d --name vcp-redis -p 6379:6379 -v redis-data:/data redis:7-alpine redis-server --appendonly yes 2>/dev/null || docker start vcp-redis
	@echo "==> Redis started on localhost:6379"

redis-stop:
	@echo "==> Stopping Redis..."
	docker stop vcp-redis 2>/dev/null || true
	@echo "==> Redis stopped"

# 开发模式：启动 Producer
dev-producer: build
	./$(BINARY_NAME) producer -w ./test/raw -s ./test/shared -o ./test/encoded --mode all -c config.yaml

# 开发模式：启动 Consumer
dev-consumer: build
	./$(BINARY_NAME) consumer -c config.yaml

# 查看预设
list-presets: build
	./$(BINARY_NAME) producer --list-presets -c config.yaml

# 帮助
help:
	@echo "VCP (Video Codec Pipeline) Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make              - Install deps and build"
	@echo "  make build        - Build the binary"
	@echo "  make build-linux  - Build for Linux amd64"
	@echo "  make build-linux-arm64  - Build for Linux arm64"
	@echo "  make build-all    - Build for all platforms"
	@echo "  make deps         - Install dependencies"
	@echo "  make clean        - Clean build artifacts"
	@echo "  make run          - Build and run"
	@echo "  make install      - Install to system"
	@echo "  make uninstall    - Uninstall from system"
	@echo "  make check        - Check dependencies"
	@echo "  make test         - Run tests"
	@echo "  make lint         - Run linter"
	@echo "  make redis-start  - Start Redis in Docker"
	@echo "  make redis-stop   - Stop Redis container"
	@echo "  make redis-cli    - Open Redis CLI"
	@echo "  make help         - Show this help"