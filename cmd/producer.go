package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"video-codec-pipeline/internal/config"
	internalredis "video-codec-pipeline/internal/redis"
)

var (
	watchDir     string
	sharedDir    string
	keepLocal    bool
	outputDir    string
	outputPrefix string
	ffmpegPreset string
	ffmpegArgs   string
	verifyOutput bool
	watchMode    string
	listPresets  bool // 列出所有可用预设
)

var producerCmd = &cobra.Command{
	Use:   "producer",
	Short: "监听目录并发布视频任务",
	Long: `Producer 监听指定目录，检测到视频文件后发布编码任务。

监听模式:
  new - 仅监听新增文件（默认）
  all - 处理现有文件 + 监听新增文件

内置 FFmpeg 预设:
  h264-nvenc    - NVIDIA H.264 编码（默认）
  h264-nvenc-hq - NVIDIA H.264 高质量
  h265-nvenc    - NVIDIA H.265 编码
  h265-nvenc-hq - NVIDIA H.265 高质量
  h264-cpu      - CPU H.264 编码
  h265-cpu      - CPU H.265 编码
  copy          - 直接复制（不重新编码）

可在配置文件中定义自定义预设，使用 --list-presets 查看所有可用预设。

示例:
  # 仅监听新文件，使用默认编码
  vcp producer -w /data/raw -s /mnt/shared -o /data/encoded

  # 处理现有文件 + 新文件
  vcp producer -w /data/raw -s /mnt/shared -o /data/encoded --mode all

  # 使用指定预设
  vcp producer -w /data/raw -s /mnt/shared -o /data/encoded -p h265-nvenc

  # 自定义 FFmpeg 参数（覆盖预设）
  vcp producer -w /data/raw -s /mnt/shared -o /data/encoded --ffmpeg-args "-c:v h264_nvenc -b:v 5M"

  # 查看所有可用预设
  vcp producer --list-presets -c config.yaml`,
	Run: runProducer,
}

func init() {
	producerCmd.Flags().StringVarP(&watchDir, "watch", "w", "", "监听目录（必需）")
	producerCmd.Flags().StringVarP(&sharedDir, "shared", "s", "", "共享存储目录（必需）")
	producerCmd.Flags().StringVarP(&outputDir, "output", "o", "", "Consumer 输出目录（必需）")
	producerCmd.Flags().StringVar(&outputPrefix, "prefix", "", "输出文件名前缀（可选）")
	producerCmd.Flags().StringVar(&watchMode, "mode", "new", "监听模式: new（仅新增）/ all（包含现有）")
	producerCmd.Flags().StringVarP(&ffmpegPreset, "preset", "p", "", "FFmpeg 预设（留空使用配置文件或默认）")
	producerCmd.Flags().StringVar(&ffmpegArgs, "ffmpeg-args", "", "自定义 FFmpeg 参数（覆盖预设）")
	producerCmd.Flags().BoolVar(&verifyOutput, "verify", true, "Consumer 是否校验输出")
	producerCmd.Flags().BoolVar(&keepLocal, "keep", false, "保留本地原文件（默认移动）")
	producerCmd.Flags().StringVarP(&configFile, "config", "c", "", "配置文件路径")
	producerCmd.Flags().BoolVar(&listPresets, "list-presets", false, "列出所有可用预设")
}

func runProducer(cmd *cobra.Command, args []string) {
	// 加载配置文件
	var cfg *config.Config
	if configFile != "" {
		var err error
		cfg, err = config.LoadConfig(configFile)
		if err != nil {
			log.Printf("加载配置失败: %v", err)
		}
	}

	// 如果只是列出预设
	if listPresets {
		printAvailablePresets(cfg)
		return
	}

	// 合并配置（命令行优先）
	if cfg != nil {
		if watchDir == "" {
			watchDir = cfg.Producer.WatchDirectory
		}
		if sharedDir == "" {
			sharedDir = cfg.Producer.SharedDirectory
		}
		if outputDir == "" {
			outputDir = cfg.Producer.OutputDirectory
		}
		if outputPrefix == "" {
			outputPrefix = cfg.Producer.OutputPrefix
		}
		if watchMode == "new" && cfg.Producer.WatchMode != "" {
			watchMode = cfg.Producer.WatchMode
		}
	}

	// 验证必需参数
	if watchDir == "" {
		log.Fatal("必须指定监听目录 (--watch)")
	}
	if sharedDir == "" {
		log.Fatal("必须指定共享存储目录 (--shared)")
	}
	if outputDir == "" {
		log.Fatal("必须指定输出目录 (--output)")
	}

	// 确定最终的 FFmpeg 参数
	finalFFmpegArgs := resolveFFmpegArgs(cfg)
	if finalFFmpegArgs == "" {
		log.Fatal("无法确定 FFmpeg 参数，请指定 --preset 或 --ffmpeg-args")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("收到退出信号...")
		cancel()
	}()

	// 获取 Redis 配置
	redisAddr := "localhost:6379"
	redisPassword := ""
	redisDB := 0
	if cfg != nil {
		redisAddr = cfg.GetRedisAddr()
		redisPassword = cfg.Redis.Password
		redisDB = cfg.Redis.DB
	}
	stream := internalredis.NewStream(redisAddr, redisPassword, redisDB)
	defer stream.Close()

	if err := stream.Ping(); err != nil {
		log.Fatalf("Redis 连接失败: %v", err)
	}

	os.MkdirAll(watchDir, 0755)
	os.MkdirAll(sharedDir, 0755)

	localIP := getLocalIP()

	log.Printf("Producer 已启动")
	log.Printf("  监听目录:   %s", watchDir)
	log.Printf("  共享存储:   %s", sharedDir)
	log.Printf("  输出目录:   %s", outputDir)
	log.Printf("  监听模式:   %s", watchMode)
	log.Printf("  FFmpeg:     %s", truncateStrProducer(finalFFmpegArgs, 60))
	log.Printf("  校验输出:   %v", verifyOutput)

	taskConfig := &taskConfiguration{
		outputDir:    outputDir,
		outputPrefix: outputPrefix,
		ffmpegArgs:   finalFFmpegArgs,
		verifyOutput: verifyOutput,
		keepLocal:    keepLocal,
		localIP:      localIP,
	}

	// 模式 all：先处理现有文件
	if watchMode == "all" {
		processExistingFiles(ctx, stream, taskConfig)
	}

	// 创建文件监听器
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("创建监听器失败: %v", err)
	}
	defer watcher.Close()

	if err := watcher.Add(watchDir); err != nil {
		log.Fatalf("添加监听目录失败: %v", err)
	}

	log.Println("开始监听新文件...")

	for {
		select {
		case <-ctx.Done():
			log.Println("Producer 退出")
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == fsnotify.Create && isVideoFile(event.Name) {
				log.Printf("检测到新文件: %s", filepath.Base(event.Name))
				go handleNewFile(ctx, stream, event.Name, taskConfig)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("监听错误: %v", err)
		}
	}
}

// resolveFFmpegArgs 解析最终的 FFmpeg 参数
// 优先级：命令行 --ffmpeg-args > 命令行 --preset > 配置文件 ffmpeg_args > 配置文件 ffmpeg_preset > 默认预设
func resolveFFmpegArgs(cfg *config.Config) string {
	// 1. 命令行直接指定参数（最高优先级）
	if ffmpegArgs != "" {
		return ffmpegArgs
	}

	// 2. 命令行指定预设
	if ffmpegPreset != "" {
		if cfg != nil {
			if args, ok := cfg.GetPreset(ffmpegPreset); ok {
				return args
			}
		}
		// 如果没有配置文件，尝试内置预设
		if args, ok := config.GetBuiltinPresets()[ffmpegPreset]; ok {
			return args
		}
		log.Printf("警告: 未知预设 '%s'，使用默认预设 h264-nvenc", ffmpegPreset)
	}

	// 3. 配置文件中的直接参数
	if cfg != nil && cfg.Producer.FFmpegArgs != "" {
		return cfg.Producer.FFmpegArgs
	}

	// 4. 配置文件中的预设名称
	if cfg != nil && cfg.Producer.FFmpegPreset != "" {
		if args, ok := cfg.GetPreset(cfg.Producer.FFmpegPreset); ok {
			return args
		}
	}

	// 5. 默认预设
	if args, ok := config.GetBuiltinPresets()["h264-nvenc"]; ok {
		return args
	}

	return ""
}

// printAvailablePresets 打印所有可用预设
func printAvailablePresets(cfg *config.Config) {
	fmt.Println("========== 可用 FFmpeg 预设 ==========")
	fmt.Println()

	// 内置预设
	fmt.Println("【内置预设】")
	builtinKeys := make([]string, 0)
	for k := range config.GetBuiltinPresets() {
		builtinKeys = append(builtinKeys, k)
	}
	sort.Strings(builtinKeys)
	for _, name := range builtinKeys {
		args := config.GetBuiltinPresets()[name]
		fmt.Printf("  %-16s %s\n", name, truncateStrProducer(args, 60))
	}

	// 自定义预设
	if cfg != nil && len(cfg.Presets) > 0 {
		fmt.Println()
		fmt.Println("【自定义预设】（来自配置文件）")
		customKeys := make([]string, 0)
		for k := range cfg.Presets {
			customKeys = append(customKeys, k)
		}
		sort.Strings(customKeys)
		for _, name := range customKeys {
			args := cfg.Presets[name]
			// 标记是否覆盖了内置预设
			suffix := ""
			if _, ok := config.GetBuiltinPresets()[name]; ok {
				suffix = " (覆盖内置)"
			}
			fmt.Printf("  %-16s %s%s\n", name, truncateStrProducer(args, 50), suffix)
		}
	}

	fmt.Println()
	fmt.Println("使用方法:")
	fmt.Println("  vcp producer -p <预设名称> ...")
	fmt.Println("  vcp producer --ffmpeg-args \"<自定义参数>\" ...")
}

// taskConfiguration 任务配置
type taskConfiguration struct {
	outputDir    string
	outputPrefix string
	ffmpegArgs   string
	verifyOutput bool
	keepLocal    bool
	localIP      string
}

// processExistingFiles 处理现有文件
func processExistingFiles(ctx context.Context, stream *internalredis.Stream, cfg *taskConfiguration) {
	entries, err := os.ReadDir(watchDir)
	if err != nil {
		log.Printf("读取目录失败: %v", err)
		return
	}

	var videoFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && isVideoFile(entry.Name()) {
			videoFiles = append(videoFiles, filepath.Join(watchDir, entry.Name()))
		}
	}

	if len(videoFiles) == 0 {
		log.Println("未找到现有视频文件")
		return
	}

	log.Printf("发现 %d 个现有视频文件，开始处理...", len(videoFiles))

	for _, filePath := range videoFiles {
		select {
		case <-ctx.Done():
			return
		default:
			handleNewFile(ctx, stream, filePath, cfg)
		}
	}
}

func handleNewFile(ctx context.Context, stream *internalredis.Stream, filePath string, cfg *taskConfiguration) {
	// 等待文件写入完成
	if err := waitFileStable(filePath, 3, 500*time.Millisecond); err != nil {
		log.Printf("文件不可用: %v", err)
		return
	}

	originalName := filepath.Base(filePath)
	taskID := fmt.Sprintf("task_%d", time.Now().UnixNano())

	// 共享存储：保持原文件名
	sharedFilePath := filepath.Join(sharedDir, originalName)

	// 检查共享存储是否已存在同名文件
	if _, err := os.Stat(sharedFilePath); err == nil {
		// 文件已存在，添加时间戳
		ext := filepath.Ext(originalName)
		base := strings.TrimSuffix(originalName, ext)
		sharedFilePath = filepath.Join(sharedDir, fmt.Sprintf("%s_%d%s", base, time.Now().UnixNano(), ext))
	}

	// 移动或复制文件
	if err := moveOrCopy(filePath, sharedFilePath, cfg.keepLocal); err != nil {
		log.Printf("传输文件失败: %v", err)
		return
	}

	// 生成输出文件名
	outputName := generateOutputName(originalName, cfg.outputPrefix)

	// 构建任务
	task := internalredis.Task{
		ID:           taskID,
		InputPath:    sharedFilePath,
		OriginalName: originalName,
		OutputDir:    cfg.outputDir,
		OutputName:   outputName,
		FFmpegArgs:   cfg.ffmpegArgs,
		VerifyOutput: cfg.verifyOutput,
		SourceIP:     cfg.localIP,
	}

	if _, err := stream.Publish(task); err != nil {
		log.Printf("发布任务失败: %v", err)
		os.Remove(sharedFilePath) // 回滚
		return
	}

	log.Printf("任务已发布: %s", originalName)
	log.Printf("  输入: %s", sharedFilePath)
	log.Printf("  输出: %s/%s", cfg.outputDir, outputName)
}

// generateOutputName 生成输出文件名
func generateOutputName(originalName, prefix string) string {
	ext := filepath.Ext(originalName)
	base := strings.TrimSuffix(originalName, ext)

	if prefix != "" {
		return fmt.Sprintf("%s_%s.mp4", prefix, base)
	}
	return base + ".mp4"
}

func moveOrCopy(src, dst string, keepSrc bool) error {
	if keepSrc {
		return copyFile(src, dst)
	}

	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}

	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func waitFileStable(path string, checks int, interval time.Duration) error {
	var lastSize int64 = -1
	stable := 0

	for stable < checks {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() == lastSize && lastSize > 0 {
			stable++
		} else {
			stable = 0
			lastSize = info.Size()
		}
		time.Sleep(interval)
	}
	return nil
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	buf := make([]byte, 4*1024*1024)
	_, err = io.CopyBuffer(dstFile, srcFile, buf)
	if err != nil {
		return err
	}

	return dstFile.Sync()
}

func isVideoFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".mp4" || ext == ".mkv" || ext == ".avi" || ext == ".mov" || ext == ".webm"
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "unknown"
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "unknown"
}

// truncateStrProducer 截断字符串（producer专用）
func truncateStrProducer(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
