# Makefile for VCP (Video Codec Pipeline)

.PHONY: all build clean deps run install test lint help

# 变量定义
BINARY_NAME := vcp
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

# 默认目标
all: deps build

# 安装依赖
deps:
	@echo "==> Installing dependencies..."
	go mod tidy
	go mod download

# 构建
build:
	@echo "==> Building $(BINARY_NAME)..."
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY_NAME) .
	@echo "==> Build complete: ./$(BINARY_NAME)"

# 构建 Linux 版本（用于服务器部署）
build-linux:
	@echo "==> Building $(BINARY_NAME) for Linux..."
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_NAME)-linux-amd64 .
	@echo "==> Build complete: ./$(BINARY_NAME)-linux-amd64"

# 清理
clean:
	@echo "==> Cleaning..."
	rm -f $(BINARY_NAME) $(BINARY_NAME)-linux-amd64
	go clean -cache

# 运行（开发用）
run: build
	./$(BINARY_NAME)

# 安装到 GOPATH/bin
install:
	@echo "==> Installing $(BINARY_NAME) to GOPATH/bin..."
	go install $(LDFLAGS) .

# 测试
test:
	@echo "==> Running tests..."
	go test -v ./...

# 代码检查
lint:
	@echo "==> Running linter..."
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run

# Docker Redis 快速启动
redis-start:
	@echo "==> Starting Redis in Docker..."
	docker run -d --name vcp-redis -p 6379:6379 -v redis-data:/data redis:7-alpine redis-server --appendonly yes
	@echo "==> Redis started on localhost:6379"

redis-stop:
	@echo "==> Stopping Redis..."
	docker stop vcp-redis && docker rm vcp-redis

redis-cli:
	@docker exec -it vcp-redis redis-cli

# 帮助
help:
	@echo "VCP (Video Codec Pipeline) Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make              - Install deps and build"
	@echo "  make build        - Build the binary"
	@echo "  make build-linux  - Build for Linux amd64"
	@echo "  make deps         - Install dependencies"
	@echo "  make clean        - Clean build artifacts"
	@echo "  make run          - Build and run"
	@echo "  make install      - Install to GOPATH/bin"
	@echo "  make test         - Run tests"
	@echo "  make lint         - Run linter"
	@echo "  make redis-start  - Start Redis in Docker"
	@echo "  make redis-stop   - Stop Redis container"
	@echo "  make redis-cli    - Open Redis CLI"
	@echo "  make help         - Show this help"