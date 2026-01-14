#!/bin/bash

# VCP - Video Codec Pipeline 安装脚本
# 支持 Linux 平台 (amd64/arm64)

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 默认配置
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/vcp"
DATA_DIR="/var/lib/vcp"
LOG_DIR="/var/log/vcp"
SYSTEMD_DIR="/etc/systemd/system"

# 版本信息
VERSION="1.0.0"
BINARY_NAME="vcp"

# 打印带颜色的消息
info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

success() {
    echo -e "${GREEN}[OK]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

# 检查是否为 root 用户
check_root() {
    if [ "$EUID" -ne 0 ]; then
        error "请使用 root 用户或 sudo 运行此脚本"
    fi
}

# 检查系统架构
check_arch() {
    ARCH=$(uname -m)
    case $ARCH in
        x86_64)
            ARCH="amd64"
            ;;
        aarch64)
            ARCH="arm64"
            ;;
        *)
            error "不支持的架构: $ARCH (仅支持 amd64/arm64)"
            ;;
    esac
    info "检测到系统架构: $ARCH"
}

# 检查操作系统
check_os() {
    if [ "$(uname -s)" != "Linux" ]; then
        error "此脚本仅支持 Linux 系统"
    fi
    
    # 检查发行版
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS_NAME=$NAME
        OS_VERSION=$VERSION_ID
        info "检测到操作系统: $OS_NAME $OS_VERSION"
    else
        warn "无法检测操作系统发行版"
    fi
}

# 检查依赖
check_dependencies() {
    info "检查依赖..."
    
    # 检查 FFmpeg
    if command -v ffmpeg &> /dev/null; then
        FFMPEG_VERSION=$(ffmpeg -version | head -n1 | awk '{print $3}')
        success "FFmpeg 已安装: $FFMPEG_VERSION"
    else
        warn "FFmpeg 未安装，编码功能将不可用"
        echo -e "  安装方法:"
        echo -e "    Ubuntu/Debian: ${GREEN}sudo apt install ffmpeg${NC}"
        echo -e "    CentOS/RHEL:   ${GREEN}sudo yum install ffmpeg${NC}"
        echo -e "    Arch Linux:    ${GREEN}sudo pacman -S ffmpeg${NC}"
    fi
    
    # 检查 NVIDIA 驱动和 NVENC
    if command -v nvidia-smi &> /dev/null; then
        NVIDIA_VERSION=$(nvidia-smi --query-gpu=driver_version --format=csv,noheader | head -n1)
        success "NVIDIA 驱动已安装: $NVIDIA_VERSION"
        
        # 检查 NVENC 支持
        if ffmpeg -hide_banner -encoders 2>/dev/null | grep -q "h264_nvenc"; then
            success "NVENC 编码器可用"
        else
            warn "NVENC 编码器不可用，将使用 CPU 编码"
        fi
    else
        warn "NVIDIA 驱动未安装，将使用 CPU 编码"
    fi
    
    # 检查 Redis
    if command -v redis-cli &> /dev/null; then
        success "Redis CLI 已安装"
    else
        warn "Redis CLI 未安装"
        echo -e "  安装方法:"
        echo -e "    Ubuntu/Debian: ${GREEN}sudo apt install redis-tools${NC}"
        echo -e "    CentOS/RHEL:   ${GREEN}sudo yum install redis${NC}"
    fi
}

# 创建目录
create_directories() {
    info "创建目录..."
    
    mkdir -p "$CONFIG_DIR"
    mkdir -p "$DATA_DIR/raw"
    mkdir -p "$DATA_DIR/shared"
    mkdir -p "$DATA_DIR/encoded"
    mkdir -p "$LOG_DIR"
    
    success "目录创建完成"
}

# 安装二进制文件
install_binary() {
    info "安装二进制文件..."
    
    BINARY_FILE="vcp-linux-${ARCH}"
    
    # 检查本地是否有编译好的二进制文件
    if [ -f "./$BINARY_FILE" ]; then
        cp "./$BINARY_FILE" "$INSTALL_DIR/$BINARY_NAME"
        chmod +x "$INSTALL_DIR/$BINARY_NAME"
        success "已安装 $INSTALL_DIR/$BINARY_NAME"
    elif [ -f "./vcp" ]; then
        cp "./vcp" "$INSTALL_DIR/$BINARY_NAME"
        chmod +x "$INSTALL_DIR/$BINARY_NAME"
        success "已安装 $INSTALL_DIR/$BINARY_NAME"
    else
        warn "未找到预编译的二进制文件，尝试编译..."
        
        # 检查 Go 环境
        if ! command -v go &> /dev/null; then
            error "Go 未安装，请先安装 Go 或提供预编译的二进制文件"
        fi
        
        info "编译中..."
        CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -ldflags="-s -w" -o "$INSTALL_DIR/$BINARY_NAME" .
        chmod +x "$INSTALL_DIR/$BINARY_NAME"
        success "编译并安装完成"
    fi
    
    # 验证安装
    if "$INSTALL_DIR/$BINARY_NAME" version &> /dev/null || "$INSTALL_DIR/$BINARY_NAME" --help &> /dev/null; then
        success "二进制文件验证通过"
    else
        warn "二进制文件可能存在问题，请手动验证"
    fi
}

# 安装配置文件
install_config() {
    info "安装配置文件..."
    
    CONFIG_FILE="$CONFIG_DIR/config.yaml"
    
    # 如果配置文件已存在，备份
    if [ -f "$CONFIG_FILE" ]; then
        BACKUP_FILE="$CONFIG_FILE.backup.$(date +%Y%m%d%H%M%S)"
        cp "$CONFIG_FILE" "$BACKUP_FILE"
        warn "已备份现有配置到: $BACKUP_FILE"
    fi
    
    # 写入默认配置
    cat > "$CONFIG_FILE" << 'EOF'
# VCP 配置文件
# 生成时间: $(date)

redis:
  address: "localhost:6379"
  password: ""
  db: 0

# 自定义 FFmpeg 预设模板
presets:
  # 自定义高码率预设
  high-bitrate: "-c:v h264_nvenc -preset p4 -b:v 20M -maxrate 25M -bufsize 40M -c:a aac -b:a 256k -movflags +faststart"
  
  # 自定义低码率预设（适合网络传输）
  low-bitrate: "-c:v h264_nvenc -preset p4 -b:v 3M -c:a aac -b:a 96k -movflags +faststart"
  
  # 4K 视频预设
  4k-nvenc: "-c:v hevc_nvenc -preset p5 -b:v 30M -maxrate 40M -bufsize 60M -c:a aac -b:a 192k -movflags +faststart"

# Producer 配置
producer:
  watch_directory: "/var/lib/vcp/raw"
  shared_directory: "/var/lib/vcp/shared"
  output_directory: "/var/lib/vcp/encoded"
  output_prefix: ""
  watch_mode: "new"
  ffmpeg_preset: "h264-nvenc"
  ffmpeg_args: ""
  verify_output: true
  keep_local: false

# Consumer 配置
consumer:
  name: ""
  concurrency: 1
EOF

    success "配置文件已安装: $CONFIG_FILE"
}

# 安装 systemd 服务
install_systemd_services() {
    info "安装 systemd 服务..."
    
    # Producer 服务
    cat > "$SYSTEMD_DIR/vcp-producer.service" << EOF
[Unit]
Description=VCP Video Codec Pipeline - Producer
After=network.target redis.service
Wants=redis.service

[Service]
Type=simple
User=root
ExecStart=$INSTALL_DIR/$BINARY_NAME producer -c $CONFIG_DIR/config.yaml
Restart=always
RestartSec=5
StandardOutput=append:$LOG_DIR/producer.log
StandardError=append:$LOG_DIR/producer.log

[Install]
WantedBy=multi-user.target
EOF

    # Consumer 服务
    cat > "$SYSTEMD_DIR/vcp-consumer.service" << EOF
[Unit]
Description=VCP Video Codec Pipeline - Consumer
After=network.target redis.service
Wants=redis.service

[Service]
Type=simple
User=root
ExecStart=$INSTALL_DIR/$BINARY_NAME consumer -c $CONFIG_DIR/config.yaml
Restart=always
RestartSec=5
StandardOutput=append:$LOG_DIR/consumer.log
StandardError=append:$LOG_DIR/consumer.log
# 如果有多 GPU，取消注释下面一行并设置
# Environment="CUDA_VISIBLE_DEVICES=0"

[Install]
WantedBy=multi-user.target
EOF

    # Consumer 多实例模板服务
    cat > "$SYSTEMD_DIR/vcp-consumer@.service" << EOF
[Unit]
Description=VCP Video Codec Pipeline - Consumer (GPU %i)
After=network.target redis.service
Wants=redis.service

[Service]
Type=simple
User=root
ExecStart=$INSTALL_DIR/$BINARY_NAME consumer -c $CONFIG_DIR/config.yaml -n gpu%i
Restart=always
RestartSec=5
StandardOutput=append:$LOG_DIR/consumer-%i.log
StandardError=append:$LOG_DIR/consumer-%i.log
Environment="CUDA_VISIBLE_DEVICES=%i"

[Install]
WantedBy=multi-user.target
EOF

    # 重新加载 systemd
    systemctl daemon-reload
    
    success "systemd 服务已安装"
    echo ""
    echo -e "  ${GREEN}启动 Producer:${NC}"
    echo -e "    sudo systemctl start vcp-producer"
    echo -e "    sudo systemctl enable vcp-producer  # 开机自启"
    echo ""
    echo -e "  ${GREEN}启动 Consumer:${NC}"
    echo -e "    sudo systemctl start vcp-consumer"
    echo -e "    sudo systemctl enable vcp-consumer  # 开机自启"
    echo ""
    echo -e "  ${GREEN}多 GPU Consumer:${NC}"
    echo -e "    sudo systemctl start vcp-consumer@0  # GPU 0"
    echo -e "    sudo systemctl start vcp-consumer@1  # GPU 1"
}

# 安装 logrotate 配置
install_logrotate() {
    info "配置日志轮转..."
    
    if [ -d "/etc/logrotate.d" ]; then
        cat > "/etc/logrotate.d/vcp" << EOF
$LOG_DIR/*.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    create 0644 root root
    sharedscripts
    postrotate
        systemctl reload vcp-producer 2>/dev/null || true
        systemctl reload vcp-consumer 2>/dev/null || true
    endscript
}
EOF
        success "日志轮转配置完成"
    else
        warn "logrotate 未安装，跳过日志轮转配置"
    fi
}

# 显示安装信息
show_info() {
    echo ""
    echo -e "${GREEN}============================================${NC}"
    echo -e "${GREEN}   VCP 安装完成！${NC}"
    echo -e "${GREEN}============================================${NC}"
    echo ""
    echo -e "  ${BLUE}二进制文件:${NC}  $INSTALL_DIR/$BINARY_NAME"
    echo -e "  ${BLUE}配置文件:${NC}    $CONFIG_DIR/config.yaml"
    echo -e "  ${BLUE}数据目录:${NC}    $DATA_DIR/"
    echo -e "  ${BLUE}日志目录:${NC}    $LOG_DIR/"
    echo ""
    echo -e "  ${YELLOW}快速开始:${NC}"
    echo ""
    echo -e "  1. 编辑配置文件:"
    echo -e "     ${GREEN}vim $CONFIG_DIR/config.yaml${NC}"
    echo ""
    echo -e "  2. 启动 Redis (如果未运行):"
    echo -e "     ${GREEN}sudo systemctl start redis${NC}"
    echo ""
    echo -e "  3. 在 Consumer 机器上启动:"
    echo -e "     ${GREEN}sudo systemctl start vcp-consumer${NC}"
    echo ""
    echo -e "  4. 在 Producer 机器上启动:"
    echo -e "     ${GREEN}sudo systemctl start vcp-producer${NC}"
    echo ""
    echo -e "  5. 查看状态:"
    echo -e "     ${GREEN}sudo systemctl status vcp-producer${NC}"
    echo -e "     ${GREEN}sudo systemctl status vcp-consumer${NC}"
    echo ""
    echo -e "  6. 查看日志:"
    echo -e "     ${GREEN}tail -f $LOG_DIR/producer.log${NC}"
    echo -e "     ${GREEN}tail -f $LOG_DIR/consumer.log${NC}"
    echo ""
    echo -e "  ${YELLOW}手动运行:${NC}"
    echo ""
    echo -e "  # Producer"
    echo -e "  ${GREEN}vcp producer -w /path/to/watch -s /path/to/shared -o /path/to/output${NC}"
    echo ""
    echo -e "  # Consumer"
    echo -e "  ${GREEN}vcp consumer${NC}"
    echo ""
    echo -e "  # 查看预设"
    echo -e "  ${GREEN}vcp producer --list-presets -c $CONFIG_DIR/config.yaml${NC}"
    echo ""
}

# 卸载函数
uninstall() {
    info "卸载 VCP..."
    
    # 停止服务
    systemctl stop vcp-producer 2>/dev/null || true
    systemctl stop vcp-consumer 2>/dev/null || true
    systemctl stop vcp-consumer@* 2>/dev/null || true
    
    systemctl disable vcp-producer 2>/dev/null || true
    systemctl disable vcp-consumer 2>/dev/null || true
    
    # 删除文件
    rm -f "$INSTALL_DIR/$BINARY_NAME"
    rm -f "$SYSTEMD_DIR/vcp-producer.service"
    rm -f "$SYSTEMD_DIR/vcp-consumer.service"
    rm -f "$SYSTEMD_DIR/vcp-consumer@.service"
    rm -f "/etc/logrotate.d/vcp"
    
    systemctl daemon-reload
    
    success "VCP 已卸载"
    echo ""
    echo -e "  ${YELLOW}以下目录未删除（可能包含数据）:${NC}"
    echo -e "    配置: $CONFIG_DIR"
    echo -e "    数据: $DATA_DIR"
    echo -e "    日志: $LOG_DIR"
    echo ""
    echo -e "  手动删除:"
    echo -e "    ${GREEN}rm -rf $CONFIG_DIR $DATA_DIR $LOG_DIR${NC}"
}

# 显示帮助
show_help() {
    echo "VCP 安装脚本"
    echo ""
    echo "用法: $0 [选项]"
    echo ""
    echo "选项:"
    echo "  install       安装 VCP (默认)"
    echo "  uninstall     卸载 VCP"
    echo "  check         仅检查依赖"
    echo "  help          显示此帮助"
    echo ""
    echo "示例:"
    echo "  sudo $0              # 安装"
    echo "  sudo $0 install      # 安装"
    echo "  sudo $0 uninstall    # 卸载"
    echo "  sudo $0 check        # 检查依赖"
}

# 主函数
main() {
    echo ""
    echo -e "${BLUE}╔════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║   VCP - Video Codec Pipeline Installer     ║${NC}"
    echo -e "${BLUE}║   Version: $VERSION                           ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════╝${NC}"
    echo ""
    
    case "${1:-install}" in
        install)
            check_root
            check_os
            check_arch
            check_dependencies
            create_directories
            install_binary
            install_config
            install_systemd_services
            install_logrotate
            show_info
            ;;
        uninstall)
            check_root
            uninstall
            ;;
        check)
            check_os
            check_arch
            check_dependencies
            ;;
        help|--help|-h)
            show_help
            ;;
        *)
            error "未知选项: $1"
            show_help
            ;;
    esac
}

main "$@"