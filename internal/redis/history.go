package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	HistoryKeyPrefix     = "vcp:history:"
	HistoryIndexKey      = "vcp:history_index"
	DefaultRetentionDays = 7
)

type TaskStatus string

const (
	StatusPending    TaskStatus = "pending"
	StatusProcessing TaskStatus = "processing"
	StatusCompleted  TaskStatus = "completed"
	StatusFailed     TaskStatus = "failed"
)

type HistoryRecord struct {
	TaskID      string        `json:"task_id"`
	FilePath    string        `json:"file_path"`
	Status      TaskStatus    `json:"status"`
	StartTime   time.Time     `json:"start_time"`
	EndTime     time.Time     `json:"end_time,omitempty"`
	Duration    time.Duration `json:"duration,omitempty"`
	RetryCount  int           `json:"retry_count"`
	ErrorMsg    string        `json:"error_msg,omitempty"`
	OutputPath  string        `json:"output_path,omitempty"`
	ProcessTime string        `json:"process_time,omitempty"`
}

type HistoryManager struct {
	client        *goredis.Client
	ctx           context.Context
	retentionDays int
}

func NewHistoryManager(client *goredis.Client, retentionDays int) *HistoryManager {
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	return &HistoryManager{
		client:        client,
		ctx:           context.Background(),
		retentionDays: retentionDays,
	}
}

// RecordTaskStart 记录任务开始
func (hm *HistoryManager) RecordTaskStart(taskID, filePath string) error {
	record := HistoryRecord{
		TaskID:    taskID,
		FilePath:  filePath,
		Status:    StatusProcessing,
		StartTime: time.Now(),
	}
	return hm.save(record)
}

// RecordTaskComplete 记录任务完成
func (hm *HistoryManager) RecordTaskComplete(taskID, outputPath string) error {
	record, err := hm.GetTaskHistory(taskID)
	if err != nil || record == nil {
		return err
	}

	record.Status = StatusCompleted
	record.EndTime = time.Now()
	record.Duration = record.EndTime.Sub(record.StartTime)
	record.ProcessTime = record.Duration.String()
	record.OutputPath = outputPath

	return hm.save(*record)
}

// RecordTaskFailed 记录任务失败
func (hm *HistoryManager) RecordTaskFailed(taskID, errorMsg string, retryCount int) error {
	record, err := hm.GetTaskHistory(taskID)
	if err != nil || record == nil {
		return err
	}

	record.Status = StatusFailed
	record.EndTime = time.Now()
	record.Duration = record.EndTime.Sub(record.StartTime)
	record.ProcessTime = record.Duration.String()
	record.ErrorMsg = errorMsg
	record.RetryCount = retryCount

	return hm.save(*record)
}

func (hm *HistoryManager) save(record HistoryRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	key := HistoryKeyPrefix + record.TaskID
	ttl := time.Duration(hm.retentionDays) * 24 * time.Hour

	pipe := hm.client.Pipeline()
	pipe.Set(hm.ctx, key, data, ttl)
	pipe.ZAdd(hm.ctx, HistoryIndexKey, goredis.Z{
		Score:  float64(record.StartTime.Unix()),
		Member: record.TaskID,
	})
	_, err = pipe.Exec(hm.ctx)
	return err
}

// GetTaskHistory 获取单个任务历史
func (hm *HistoryManager) GetTaskHistory(taskID string) (*HistoryRecord, error) {
	data, err := hm.client.Get(hm.ctx, HistoryKeyPrefix+taskID).Bytes()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var record HistoryRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// GetAllHistory 获取指定时间范围内的历史记录
func (hm *HistoryManager) GetAllHistory(startTime, endTime time.Time) ([]HistoryRecord, error) {
	taskIDs, err := hm.client.ZRangeByScore(hm.ctx, HistoryIndexKey, &goredis.ZRangeBy{
		Min: fmt.Sprintf("%d", startTime.Unix()),
		Max: fmt.Sprintf("%d", endTime.Unix()),
	}).Result()
	if err != nil {
		return nil, err
	}

	var records []HistoryRecord
	for _, taskID := range taskIDs {
		if record, err := hm.GetTaskHistory(taskID); err == nil && record != nil {
			records = append(records, *record)
		}
	}
	return records, nil
}

// GetStats 获取统计信息
func (hm *HistoryManager) GetStats(startTime, endTime time.Time) (map[string]interface{}, error) {
	records, err := hm.GetAllHistory(startTime, endTime)
	if err != nil {
		return nil, err
	}

	stats := map[string]interface{}{
		"total":     len(records),
		"completed": 0,
		"failed":    0,
		"pending":   0,
	}

	var totalDuration time.Duration
	completedCount := 0

	for _, r := range records {
		switch r.Status {
		case StatusCompleted:
			stats["completed"] = stats["completed"].(int) + 1
			totalDuration += r.Duration
			completedCount++
		case StatusFailed:
			stats["failed"] = stats["failed"].(int) + 1
		default:
			stats["pending"] = stats["pending"].(int) + 1
		}
	}

	if completedCount > 0 {
		stats["avg_duration"] = (totalDuration / time.Duration(completedCount)).String()
	} else {
		stats["avg_duration"] = "N/A"
	}

	return stats, nil
}
