package redis

import (
	"context"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	DefaultStreamName    = "vcp:tasks"
	DefaultConsumerGroup = "gpu_encoders"
	MaxRetryCount        = 3
)

type Stream struct {
	Client *goredis.Client
	ctx    context.Context
}

// Task 任务结构 - 由 Producer 完全定义
type Task struct {
	// 基础信息
	ID        string // 任务 ID
	MessageID string // Redis 消息 ID
	SourceIP  string // 来源 Producer IP
	Retry     int    // 重试次数

	// 文件信息
	InputPath    string // 共享存储中的输入文件路径
	OriginalName string // 原始文件名

	// Producer 指定的输出配置
	OutputDir  string // Consumer 输出目录
	OutputName string // 输出文件名（含扩展名）

	// Producer 指定的编码配置
	FFmpegArgs   string // FFmpeg 参数（不含 ffmpeg 命令和输入输出）
	VerifyOutput bool   // 是否校验输出
}

func NewStream(addr, password string, db int) *Stream {
	rdb := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	return &Stream{Client: rdb, ctx: context.Background()}
}

// CreateConsumerGroup 创建消费者组
func (s *Stream) CreateConsumerGroup(stream, group string) error {
	err := s.Client.XGroupCreateMkStream(s.ctx, stream, group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}
	return nil
}

// Publish 发布任务
func (s *Stream) Publish(task Task) (string, error) {
	return s.Client.XAdd(s.ctx, &goredis.XAddArgs{
		Stream: DefaultStreamName,
		Values: map[string]interface{}{
			"task_id":       task.ID,
			"input_path":    task.InputPath,
			"original_name": task.OriginalName,
			"output_dir":    task.OutputDir,
			"output_name":   task.OutputName,
			"ffmpeg_args":   task.FFmpegArgs,
			"verify_output": boolToStr(task.VerifyOutput),
			"source_ip":     task.SourceIP,
			"retry":         task.Retry,
		},
	}).Result()
}

// ReadGroup 从消费者组读取任务
func (s *Stream) ReadGroup(group, consumer string, count int64, block time.Duration) ([]Task, error) {
	results, err := s.Client.XReadGroup(s.ctx, &goredis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{DefaultStreamName, ">"},
		Count:    count,
		Block:    block,
	}).Result()

	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return s.parseMessages(results), nil
}

// ReadPendingTasks 读取 pending 任务（用于故障恢复）
func (s *Stream) ReadPendingTasks(group, consumer string, count int64) ([]Task, error) {
	results, err := s.Client.XReadGroup(s.ctx, &goredis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{DefaultStreamName, "0"},
		Count:    count,
	}).Result()

	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return s.parseMessages(results), nil
}

func (s *Stream) parseMessages(results []goredis.XStream) []Task {
	var tasks []Task
	for _, result := range results {
		for _, msg := range result.Messages {
			task := Task{MessageID: msg.ID}
			if v, ok := msg.Values["task_id"].(string); ok {
				task.ID = v
			}
			if v, ok := msg.Values["input_path"].(string); ok {
				task.InputPath = v
			}
			if v, ok := msg.Values["original_name"].(string); ok {
				task.OriginalName = v
			}
			if v, ok := msg.Values["output_dir"].(string); ok {
				task.OutputDir = v
			}
			if v, ok := msg.Values["output_name"].(string); ok {
				task.OutputName = v
			}
			if v, ok := msg.Values["ffmpeg_args"].(string); ok {
				task.FFmpegArgs = v
			}
			if v, ok := msg.Values["verify_output"].(string); ok {
				task.VerifyOutput = v == "true" || v == "1"
			}
			if v, ok := msg.Values["source_ip"].(string); ok {
				task.SourceIP = v
			}
			if v, ok := msg.Values["retry"].(string); ok {
				task.Retry, _ = strconv.Atoi(v)
			}
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// Acknowledge 确认任务完成
func (s *Stream) Acknowledge(group, messageID string) error {
	return s.Client.XAck(s.ctx, DefaultStreamName, group, messageID).Err()
}

// Retry 重新发布任务（用于重试）
func (s *Stream) Retry(task Task) error {
	task.Retry++
	_, err := s.Publish(task)
	return err
}

func (s *Stream) Ping() error {
	return s.Client.Ping(s.ctx).Err()
}

func (s *Stream) Close() error {
	return s.Client.Close()
}

func boolToStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
