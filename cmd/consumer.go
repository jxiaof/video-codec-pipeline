package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"video-codec-pipeline/internal/config"
	internalredis "video-codec-pipeline/internal/redis"
)

var (
	consumerName string
	concurrency  int
)

var consumerCmd = &cobra.Command{
	Use:   "consumer",
	Short: "消费任务并编码视频",
	Long: `Consumer 从 Redis Stream 获取任务并执行编码。

所有编码参数（输出目录、文件名、FFmpeg 参数）均由 Producer 指定，
Consumer 只负责执行。失败任务直接丢弃，不会重试。

示例:
  # 启动单个 Consumer
  vcp consumer

  # 指定名称和并发数
  vcp consumer -n gpu0 -j 2

  # 多 GPU 部署
  CUDA_VISIBLE_DEVICES=0 vcp consumer -n gpu0
  CUDA_VISIBLE_DEVICES=1 vcp consumer -n gpu1`,
	Run: runConsumer,
}

func init() {
	consumerCmd.Flags().StringVarP(&consumerName, "name", "n", "", "消费者名称（默认自动生成）")
	consumerCmd.Flags().IntVarP(&concurrency, "concurrency", "j", 1, "并发数（默认 1）")
	consumerCmd.Flags().StringVarP(&configFile, "config", "c", "", "配置文件")
}

func runConsumer(cmd *cobra.Command, args []string) {
	var cfg *config.Config
	if configFile != "" {
		var err error
		cfg, err = config.LoadConfig(configFile)
		if err != nil {
			log.Printf("加载配置失败: %v", err)
		}
	}

	if cfg != nil {
		if consumerName == "" && cfg.Consumer.Name != "" {
			consumerName = cfg.Consumer.Name
		}
		if concurrency == 1 && cfg.Consumer.Concurrency > 0 {
			concurrency = cfg.Consumer.Concurrency
		}
	}

	if consumerName == "" {
		hostname, _ := os.Hostname()
		consumerName = fmt.Sprintf("consumer_%s_%d", hostname, os.Getpid())
	}

	// 使用带取消的 context
	ctx, cancel := context.WithCancel(context.Background())

	// 信号处理 - 确保能正确退出
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

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

	if err := stream.Ping(); err != nil {
		log.Fatalf("Redis 连接失败: %v", err)
	}

	stream.CreateConsumerGroup(internalredis.DefaultStreamName, internalredis.DefaultConsumerGroup)

	log.Printf("Consumer [%s] 已启动", consumerName)
	log.Printf("  并发数: %d", concurrency)
	log.Printf("  Redis:  %s", redisAddr)
	log.Println("  等待 Producer 分配任务...")
	log.Println("  按 Ctrl+C 退出")

	// 统计信息（使用原子操作）
	var totalProcessed, totalSuccess, totalFailed int64
	startupTime := time.Now()

	// 工作通道和同步
	taskCh := make(chan internalredis.Task, concurrency*2)
	var wg sync.WaitGroup

	// 启动 worker
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case task, ok := <-taskCh:
					if !ok {
						return
					}
					// 验证任务有效性
					if task.ID == "" || task.InputPath == "" {
						log.Printf("[Worker %d] 跳过无效任务", id)
						if task.MessageID != "" {
							stream.Acknowledge(internalredis.DefaultConsumerGroup, task.MessageID)
						}
						continue
					}
					log.Printf("[Worker %d] 处理: %s (%s)", id, task.ID, task.OriginalName)
					success := processTask(ctx, stream, task)
					atomic.AddInt64(&totalProcessed, 1)
					if success {
						atomic.AddInt64(&totalSuccess, 1)
					} else {
						atomic.AddInt64(&totalFailed, 1)
					}
				}
			}
		}(i)
	}

	// 读取任务的 goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				tasks, err := stream.ReadGroup(internalredis.DefaultConsumerGroup, consumerName, 1, 3*time.Second)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("读取任务失败: %v", err)
					time.Sleep(time.Second)
					continue
				}
				for _, task := range tasks {
					select {
					case taskCh <- task:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	// 等待退出信号
	<-sigCh
	log.Println()
	log.Println("收到退出信号，正在优雅关闭...")

	// 取消 context
	cancel()

	// 关闭任务通道
	close(taskCh)

	// 等待所有 worker 完成（最多等待 5 秒）
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("所有任务已完成")
	case <-time.After(5 * time.Second):
		log.Println("等待超时，强制退出")
	}

	// 关闭 Redis 连接
	stream.Close()

	// 打印统计
	elapsed := time.Since(startupTime).Round(time.Second)
	log.Println()
	log.Println("========== Consumer 统计 ==========")
	log.Printf("运行时长: %s", elapsed)
	log.Printf("处理总数: %d", atomic.LoadInt64(&totalProcessed))
	log.Printf("成功: %d", atomic.LoadInt64(&totalSuccess))
	log.Printf("失败: %d", atomic.LoadInt64(&totalFailed))
	log.Println("====================================")
	log.Println("Consumer 已退出")
}

// processTask 处理单个任务，返回是否成功
// 失败任务直接丢弃，不重试
func processTask(ctx context.Context, stream *internalredis.Stream, task internalredis.Task) bool {
	taskStartTime := time.Now()

	// 打印任务信息
	log.Println()
	log.Printf("┌─────────────────────────────────────────────────────")
	log.Printf("│ 任务开始: %s", task.ID)
	log.Printf("│ 原始文件: %s", task.OriginalName)
	log.Printf("│ 输入路径: %s", task.InputPath)
	log.Printf("│ 输出目录: %s", task.OutputDir)
	log.Printf("│ 输出文件: %s", task.OutputName)
	log.Printf("├─────────────────────────────────────────────────────")

	// 检查 context 是否已取消
	if ctx.Err() != nil {
		log.Printf("│ [取消] 任务被取消")
		log.Printf("└─────────────────────────────────────────────────────")
		return false
	}

	// 等待文件可读（带超时和取消）
	waitStart := time.Now()
	log.Printf("│ [等待] 检查输入文件...")
	if err := waitForFileWithContext(ctx, task.InputPath, 30*time.Second); err != nil {
		waitDuration := time.Since(waitStart).Round(time.Millisecond)
		log.Printf("│ [失败] 文件不可用 (耗时: %s)", waitDuration)
		log.Printf("│ [错误] %v", err)
		log.Printf("│ [丢弃] 任务失败，直接丢弃不重试")
		log.Printf("│ 总耗时: %s", time.Since(taskStartTime).Round(time.Millisecond))
		log.Printf("└─────────────────────────────────────────────────────")
		stream.Acknowledge(internalredis.DefaultConsumerGroup, task.MessageID)
		return false
	}
	waitDuration := time.Since(waitStart).Round(time.Millisecond)
	log.Printf("│ [完成] 文件就绪 (耗时: %s)", waitDuration)

	// 确保输出目录存在
	if err := os.MkdirAll(task.OutputDir, 0755); err != nil {
		log.Printf("│ [失败] 创建输出目录失败: %v", err)
		log.Printf("│ [丢弃] 任务失败，直接丢弃不重试")
		log.Printf("│ 总耗时: %s", time.Since(taskStartTime).Round(time.Millisecond))
		log.Printf("└─────────────────────────────────────────────────────")
		stream.Acknowledge(internalredis.DefaultConsumerGroup, task.MessageID)
		return false
	}

	outputPath := filepath.Join(task.OutputDir, task.OutputName)

	// 执行 FFmpeg（带超时）
	log.Printf("│ [编码] 开始执行 FFmpeg...")
	encodeStart := time.Now()
	if err := runFFmpegWithTimeout(ctx, task.InputPath, outputPath, task.FFmpegArgs, 60*time.Minute); err != nil {
		encodeDuration := time.Since(encodeStart).Round(time.Second)
		log.Printf("│ [失败] 编码失败 (耗时: %s)", encodeDuration)
		log.Printf("│ [错误] %v", err)
		log.Printf("│ [丢弃] 任务失败，直接丢弃不重试")
		log.Printf("│ 总耗时: %s", time.Since(taskStartTime).Round(time.Second))
		log.Printf("└─────────────────────────────────────────────────────")
		os.Remove(outputPath)
		stream.Acknowledge(internalredis.DefaultConsumerGroup, task.MessageID)
		return false
	}
	encodeDuration := time.Since(encodeStart).Round(time.Second)
	log.Printf("│ [完成] 编码成功 (耗时: %s)", encodeDuration)

	// 校验（如果 Producer 要求）
	if task.VerifyOutput {
		log.Printf("│ [校验] 验证输出文件...")
		verifyStart := time.Now()
		if err := verifyOutputFile(outputPath); err != nil {
			verifyDuration := time.Since(verifyStart).Round(time.Millisecond)
			log.Printf("│ [失败] 校验失败 (耗时: %s)", verifyDuration)
			log.Printf("│ [错误] %v", err)
			log.Printf("│ [丢弃] 任务失败，直接丢弃不重试")
			log.Printf("│ 总耗时: %s", time.Since(taskStartTime).Round(time.Second))
			log.Printf("└─────────────────────────────────────────────────────")
			os.Remove(outputPath)
			stream.Acknowledge(internalredis.DefaultConsumerGroup, task.MessageID)
			return false
		}
		verifyDuration := time.Since(verifyStart).Round(time.Millisecond)
		log.Printf("│ [完成] 校验通过 (耗时: %s)", verifyDuration)
	}

	// 获取输出文件大小
	var outputSize string
	if info, err := os.Stat(outputPath); err == nil {
		outputSize = formatFileSize(info.Size())
	}

	// 删除源文件
	if err := os.Remove(task.InputPath); err != nil {
		log.Printf("│ [警告] 删除源文件失败: %v", err)
	} else {
		log.Printf("│ [清理] 已删除源文件")
	}

	// ACK
	stream.Acknowledge(internalredis.DefaultConsumerGroup, task.MessageID)

	totalDuration := time.Since(taskStartTime).Round(time.Second)
	log.Printf("├─────────────────────────────────────────────────────")
	log.Printf("│ [成功] 任务完成!")
	log.Printf("│ 输出文件: %s", outputPath)
	log.Printf("│ 文件大小: %s", outputSize)
	log.Printf("│ 编码耗时: %s", encodeDuration)
	log.Printf("│ 总耗时:   %s", totalDuration)
	log.Printf("└─────────────────────────────────────────────────────")

	return true
}

// waitForFileWithContext 带 context 的文件等待
func waitForFileWithContext(ctx context.Context, path string, timeout time.Duration) error {
	if path == "" {
		return fmt.Errorf("文件路径为空")
	}

	deadline := time.Now().Add(timeout)
	var lastSize int64 = -1
	stableCount := 0

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return fmt.Errorf("检查文件失败: %w", err)
		}

		if info.Size() > 0 {
			if info.Size() == lastSize {
				stableCount++
				if stableCount >= 3 {
					// 再次确认文件可读
					f, err := os.Open(path)
					if err != nil {
						return fmt.Errorf("文件无法打开: %w", err)
					}
					f.Close()
					return nil
				}
			} else {
				stableCount = 0
				lastSize = info.Size()
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("等待文件超时 (%s): %s", timeout, path)
}

// runFFmpegWithTimeout 带超时的 FFmpeg 执行
func runFFmpegWithTimeout(ctx context.Context, input, output, ffmpegArgs string, timeout time.Duration) error {
	// 创建带超时的 context
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 构建命令
	args := []string{"-hide_banner", "-loglevel", "warning", "-y", "-i", input}
	if ffmpegArgs != "" {
		args = append(args, strings.Fields(ffmpegArgs)...)
	}
	args = append(args, output)

	cmd := exec.CommandContext(timeoutCtx, "ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 打印命令（截断显示）
	cmdStr := "ffmpeg " + strings.Join(args, " ")
	if len(cmdStr) > 100 {
		cmdStr = cmdStr[:97] + "..."
	}
	log.Printf("│ [命令] %s", cmdStr)

	err := cmd.Run()
	if timeoutCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("编码超时 (>%s)", timeout)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("任务被取消")
	}
	return err
}

func verifyOutputFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("输出文件不存在: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("输出文件为空")
	}

	// 使用 ffprobe 验证
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_type", "-of", "csv=p=0", path)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ffprobe 验证失败: %w", err)
	}
	if !strings.Contains(string(out), "video") {
		return fmt.Errorf("无有效视频流")
	}
	return nil
}

func formatFileSize(size int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case size >= GB:
		return fmt.Sprintf("%.2f GB", float64(size)/float64(GB))
	case size >= MB:
		return fmt.Sprintf("%.2f MB", float64(size)/float64(MB))
	case size >= KB:
		return fmt.Sprintf("%.2f KB", float64(size)/float64(KB))
	default:
		return fmt.Sprintf("%d B", size)
	}
}
