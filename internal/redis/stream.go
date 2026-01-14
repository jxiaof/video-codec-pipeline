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
	MaxStreamLength      = 1000 // 保留最近 1000 条消息
)

type Stream struct {
	Client *goredis.Client
	Ctx    context.Context
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

// QueueInfo 队列信息
type QueueInfo struct {
	Length  int64 // Stream 总长度（包含已消费）
	Pending int64 // 待处理任务数
	Groups  int   // 消费者组数
}

func NewStream(addr, password string, db int) *Stream {
	rdb := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	return &Stream{Client: rdb, Ctx: context.Background()}
}

// CreateConsumerGroup 创建消费者组
func (s *Stream) CreateConsumerGroup(stream, group string) error {
	err := s.Client.XGroupCreateMkStream(s.Ctx, stream, group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}
	return nil
}

// Publish 发布任务
func (s *Stream) Publish(task Task) (string, error) {
	return s.Client.XAdd(s.Ctx, &goredis.XAddArgs{
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
	results, err := s.Client.XReadGroup(s.Ctx, &goredis.XReadGroupArgs{
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
	results, err := s.Client.XReadGroup(s.Ctx, &goredis.XReadGroupArgs{
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

// Acknowledge 确认任务完成（ACK + 删除消息）
func (s *Stream) Acknowledge(group, messageID string) error {
	// 先 ACK
	if err := s.Client.XAck(s.Ctx, DefaultStreamName, group, messageID).Err(); err != nil {
		return err
	}
	// 再删除消息（释放空间）
	s.Client.XDel(s.Ctx, DefaultStreamName, messageID)
	return nil
}

// AcknowledgeOnly 仅 ACK 不删除（用于需要保留历史的场景）
func (s *Stream) AcknowledgeOnly(group, messageID string) error {
	return s.Client.XAck(s.Ctx, DefaultStreamName, group, messageID).Err()
}

// Retry 重新发布任务（用于重试）
func (s *Stream) Retry(task Task) error {
	task.Retry++
	_, err := s.Publish(task)
	return err
}

func (s *Stream) Ping() error {
	return s.Client.Ping(s.Ctx).Err()
}

func (s *Stream) Close() error {
	return s.Client.Close()
}

// GetQueueInfo 获取队列信息（显示真实待处理数）
func (s *Stream) GetQueueInfo() (*QueueInfo, error) {
	info := &QueueInfo{}

	// 获取 Stream 长度（未消费的消息数）
	length, err := s.Client.XLen(s.Ctx, DefaultStreamName).Result()
	if err != nil && err != goredis.Nil {
		return info, nil
	}
	info.Length = length

	// 获取消费者组信息
	groups, err := s.Client.XInfoGroups(s.Ctx, DefaultStreamName).Result()
	if err != nil && err != goredis.Nil {
		return info, nil
	}
	info.Groups = len(groups)

	// 计算 pending 任务数（正在处理但未 ACK 的）
	for _, g := range groups {
		info.Pending += g.Pending
	}

	return info, nil
}

// GetRealQueueLength 获取真正待消费的任务数
// = Stream 长度 - 已被读取但未 ACK 的（pending）
func (s *Stream) GetRealQueueLength() (int64, error) {
	// 获取 Stream 长度
	length, err := s.Client.XLen(s.Ctx, DefaultStreamName).Result()
	if err != nil {
		if err == goredis.Nil {
			return 0, nil
		}
		return 0, err
	}
	return length, nil
}

// TrimStream 清理 Stream，保留最近 N 条消息
func (s *Stream) TrimStream(maxLen int64) (int64, error) {
	return s.Client.XTrimMaxLen(s.Ctx, DefaultStreamName, maxLen).Result()
}

// DeleteStream 删除整个 Stream
func (s *Stream) DeleteStream() error {
	return s.Client.Del(s.Ctx, DefaultStreamName).Err()
}

// DeleteHistory 删除历史记录
func (s *Stream) DeleteHistory() (int64, error) {
	var count int64

	// 删除历史索引
	if err := s.Client.Del(s.Ctx, HistoryIndexKey).Err(); err != nil {
		return count, err
	}

	// 删除所有历史记录键
	iter := s.Client.Scan(s.Ctx, 0, HistoryKeyPrefix+"*", 1000).Iterator()
	var keys []string
	for iter.Next(s.Ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return count, err
	}

	if len(keys) > 0 {
		if err := s.Client.Del(s.Ctx, keys...).Err(); err != nil {
			return count, err
		}
		count = int64(len(keys))
	}
	return count, nil
}

// CleanPendingTasks 清理所有 pending 任务
func (s *Stream) CleanPendingTasks(group string) (int64, error) {
	pending, err := s.Client.XPendingExt(s.Ctx, &goredis.XPendingExtArgs{
		Stream: DefaultStreamName,
		Group:  group,
		Start:  "-",
		End:    "+",
		Count:  10000,
	}).Result()

	if err != nil {
		if err == goredis.Nil {
			return 0, nil
		}
		return 0, err
	}

	var count int64
	for _, p := range pending {
		// ACK + 删除
		if err := s.Acknowledge(group, p.ID); err == nil {
			count++
		}
	}

	return count, nil
}

// GetPendingTasks 获取 pending 任务列表
func (s *Stream) GetPendingTasks(group string, count int64) ([]goredis.XPendingExt, error) {
	pending, err := s.Client.XPendingExt(s.Ctx, &goredis.XPendingExtArgs{
		Stream: DefaultStreamName,
		Group:  group,
		Start:  "-",
		End:    "+",
		Count:  count,
	}).Result()

	if err == goredis.Nil {
		return nil, nil
	}
	return pending, err
}

// GetStreamInfo 获取 Stream 详细信息
func (s *Stream) GetStreamInfo() (map[string]interface{}, error) {
	info := make(map[string]interface{})

	streamInfo, err := s.Client.XInfoStream(s.Ctx, DefaultStreamName).Result()
	if err != nil {
		if err == goredis.Nil {
			info["exists"] = false
			return info, nil
		}
		return nil, err
	}

	info["exists"] = true
	info["length"] = streamInfo.Length
	info["first_entry"] = streamInfo.FirstEntry.ID
	info["last_entry"] = streamInfo.LastEntry.ID

	return info, nil
}

// GetConsumerGroups 获取消费者组列表
func (s *Stream) GetConsumerGroups() ([]goredis.XInfoGroup, error) {
	groups, err := s.Client.XInfoGroups(s.Ctx, DefaultStreamName).Result()
	if err == goredis.Nil {
		return nil, nil
	}
	return groups, err
}

// GetConsumers 获取指定组的消费者列表
func (s *Stream) GetConsumers(group string) ([]goredis.XInfoConsumer, error) {
	consumers, err := s.Client.XInfoConsumers(s.Ctx, DefaultStreamName, group).Result()
	if err == goredis.Nil {
		return nil, nil
	}
	return consumers, err
}

func boolToStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
