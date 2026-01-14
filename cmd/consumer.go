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
Consumer 只负责执行。

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("收到退出信号...")
		cancel()
	}()

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

	stream.CreateConsumerGroup(internalredis.DefaultStreamName, internalredis.DefaultConsumerGroup)

	log.Printf("Consumer [%s] 已启动", consumerName)
	log.Printf("  并发数: %d", concurrency)
	log.Println("  等待 Producer 分配任务...")

	// 恢复 pending 任务
	if tasks, _ := stream.ReadPendingTasks(internalredis.DefaultConsumerGroup, consumerName, 100); len(tasks) > 0 {
		log.Printf("恢复 %d 个 pending 任务", len(tasks))
		for _, task := range tasks {
			processTask(ctx, stream, task)
		}
	}

	taskCh := make(chan internalredis.Task, concurrency*2)

	for i := 0; i < concurrency; i++ {
		go func(id int) {
			for task := range taskCh {
				log.Printf("[Worker %d] 处理: %s", id, task.OriginalName)
				processTask(ctx, stream, task)
			}
		}(i)
	}

	for {
		select {
		case <-ctx.Done():
			close(taskCh)
			log.Println("Consumer 退出")
			return
		default:
			tasks, err := stream.ReadGroup(internalredis.DefaultConsumerGroup, consumerName, 1, 5*time.Second)
			if err != nil {
				log.Printf("读取任务失败: %v", err)
				time.Sleep(time.Second)
				continue
			}
			for _, task := range tasks {
				taskCh <- task
			}
		}
	}
}

func processTask(ctx context.Context, stream *internalredis.Stream, task internalredis.Task) {
	startTime := time.Now()

	// 打印任务信息
	log.Printf("任务 %s:", task.ID)
	log.Printf("  输入: %s", task.InputPath)
	log.Printf("  输出: %s/%s", task.OutputDir, task.OutputName)

	// 等待文件可读
	if err := waitForFile(task.InputPath, 30*time.Second); err != nil {
		log.Printf("任务 %s: 文件不可用: %v", task.ID, err)
		handleFailure(stream, task, err.Error())
		return
	}

	// 确保输出目录存在
	if err := os.MkdirAll(task.OutputDir, 0755); err != nil {
		log.Printf("任务 %s: 创建输出目录失败: %v", task.ID, err)
		handleFailure(stream, task, err.Error())
		return
	}

	outputPath := filepath.Join(task.OutputDir, task.OutputName)

	// 执行 FFmpeg（使用 Producer 指定的参数）
	if err := runFFmpeg(ctx, task.InputPath, outputPath, task.FFmpegArgs); err != nil {
		log.Printf("任务 %s: 编码失败: %v", task.ID, err)
		os.Remove(outputPath)
		handleFailure(stream, task, err.Error())
		return
	}

	// 校验（如果 Producer 要求）
	if task.VerifyOutput {
		if err := verifyOutputFile(outputPath); err != nil {
			log.Printf("任务 %s: 校验失败: %v", task.ID, err)
			os.Remove(outputPath)
			handleFailure(stream, task, err.Error())
			return
		}
	}

	// 删除源文件
	if err := os.Remove(task.InputPath); err != nil {
		log.Printf("任务 %s: 删除源文件失败: %v", task.ID, err)
	}

	// ACK
	stream.Acknowledge(internalredis.DefaultConsumerGroup, task.MessageID)

	duration := time.Since(startTime).Round(time.Second)
	log.Printf("任务 %s 完成 [%s]: %s", task.ID, duration, outputPath)
}

func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastSize int64 = -1

	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err == nil && info.Size() > 0 {
			if info.Size() == lastSize {
				return nil
			}
			lastSize = info.Size()
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("文件不可用: %s", path)
}

func runFFmpeg(ctx context.Context, input, output, ffmpegArgs string) error {
	// 构建完整命令：ffmpeg -hide_banner -loglevel warning -i INPUT [ARGS] -y OUTPUT
	args := []string{"-hide_banner", "-loglevel", "warning", "-i", input}

	// 添加 Producer 指定的参数
	if ffmpegArgs != "" {
		args = append(args, strings.Fields(ffmpegArgs)...)
	}

	// 添加输出
	args = append(args, "-y", output)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("执行: ffmpeg %s", strings.Join(args, " "))
	return cmd.Run()
}

func verifyOutputFile(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return fmt.Errorf("输出文件无效")
	}

	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_type", "-of", "csv=p=0", path)
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "video") {
		return fmt.Errorf("无有效视频流")
	}
	return nil
}

func handleFailure(stream *internalredis.Stream, task internalredis.Task, errMsg string) {
	if task.Retry < internalredis.MaxRetryCount {
		log.Printf("任务 %s 重试 (%d/%d)", task.ID, task.Retry+1, internalredis.MaxRetryCount)
		stream.Retry(task)
	} else {
		log.Printf("任务 %s 失败: %s", task.ID, errMsg)
		stream.Acknowledge(internalredis.DefaultConsumerGroup, task.MessageID)
	}
}
