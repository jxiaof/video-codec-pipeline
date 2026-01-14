# VCP - Video Codec Pipeline

基于共享存储的分布式视频编码流水线。

## 架构

```
Producer (H800) 决定一切:
├── 输入文件
├── 输出目录
├── 输出文件名
├── FFmpeg 参数
└── 是否校验

Consumer (5090) 只负责执行
```

## 文件流转

```
1. Producer 监听到文件      → /data/raw/video.mp4
2. 移动到共享存储           → /mnt/shared/video.mp4  (文件名不变)
3. Consumer 编码输出        → /data/encoded/video.mp4
4. 删除共享存储源文件       → /mnt/shared/video.mp4 (删除)
```

## 快速开始

```bash
# 1. 启动 Redis
make redis-start

# 2. 构建
make build

# 3. Consumer 端（RTX 5090）
./vcp consumer

# 4. Producer 端（H800）
./vcp producer \
  -w /data/raw \
  -s /mnt/shared \
  -o /data/encoded
```

## Producer 命令

```bash
vcp producer [flags]

必需参数:
  -w, --watch string     监听目录
  -s, --shared string    共享存储目录
  -o, --output string    Consumer 输出目录

可选参数:
      --mode string      监听模式: new(默认) / all
      --prefix string    输出文件名前缀
  -p, --preset string    FFmpeg 预设 (默认 h264-nvenc)
      --ffmpeg-args      自定义 FFmpeg 参数
      --verify           校验输出 (默认 true)
      --keep             保留本地原文件
```

### FFmpeg 预设

| 预设 | 说明 |
|------|------|
| `h264-nvenc` | NVIDIA H.264 编码（默认） |
| `h264-nvenc-hq` | NVIDIA H.264 高质量 |
| `h265-nvenc` | NVIDIA H.265 编码 |
| `h265-nvenc-hq` | NVIDIA H.265 高质量 |
| `h264-cpu` | CPU H.264 编码 |
| `copy` | 直接复制 |

### 监听模式

```bash
# 仅监听新文件（默认）
./vcp producer -w /data/raw -s /mnt/shared -o /data/out --mode new

# 处理现有文件 + 监听新文件
./vcp producer -w /data/raw -s /mnt/shared -o /data/out --mode all
```

## Consumer 命令

```bash
vcp consumer [flags]

可选参数:
  -n, --name string      消费者名称
  -j, --concurrency int  并发数 (默认 1)
```

Consumer 只需启动即可，所有编码参数由 Producer 指定。

## 多节点部署

```bash
# H800 节点 1
./vcp producer -w /data/node1 -s /mnt/shared -o /data/out

# H800 节点 2 (不同输出前缀区分)
./vcp producer -w /data/node2 -s /mnt/shared -o /data/out --prefix node2

# RTX 5090 节点
CUDA_VISIBLE_DEVICES=0 ./vcp consumer -n gpu0
CUDA_VISIBLE_DEVICES=1 ./vcp consumer -n gpu1
```

## Mermaid 时序图

```mermaid
sequenceDiagram
    participant P as Producer (H800)
    participant S as 共享存储 (NFS)
    participant R as Redis Stream
    participant C as Consumer (5090)
    participant O as 输出目录

    P->>P: 检测到新文件 video.mp4
    P->>P: 等待写入完成
    P->>S: 移动文件 (保持原名)
    P->>R: 发布任务<br/>{input, output_dir, output_name, ffmpeg_args}
    
    C->>R: 获取任务
    C->>S: 读取视频
    C->>C: 执行 FFmpeg (使用 Producer 指定的参数)
    C->>O: 保存到 Producer 指定的目录和文件名
    C->>C: 校验 (如果 Producer 要求)
    C->>S: 删除源文件
    C->>R: ACK 完成
```