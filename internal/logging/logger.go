package logging

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// LogLevel 日志级别
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

var (
	logLevelName = map[LogLevel]string{
		DEBUG: "DEBUG",
		INFO:  "INFO",
		WARN:  "WARN",
		ERROR: "ERROR",
	}
	// 全局日志级别，默认 INFO
	globalLogLevel = INFO
)

// SetLogLevel 设置全局日志级别
func SetLogLevel(level string) {
	switch strings.ToUpper(level) {
	case "DEBUG":
		globalLogLevel = DEBUG
	case "INFO":
		globalLogLevel = INFO
	case "WARN":
		globalLogLevel = WARN
	case "ERROR":
		globalLogLevel = ERROR
	default:
		globalLogLevel = INFO
	}
}

// Logger 结构化日志
type Logger struct {
	component string // 组件名（producer/consumer/stats）
}

// NewLogger 创建新的日志对象
func NewLogger(component string) *Logger {
	return &Logger{component: component}
}

// log 内部日志函数
func (l *Logger) log(level LogLevel, message string, args ...interface{}) {
	// 检查日志级别
	if level < globalLogLevel {
		return
	}

	levelName := logLevelName[level]
	prefix := fmt.Sprintf("[%s] [%s]", levelName, l.component)

	var output string
	if len(args) > 0 {
		output = fmt.Sprintf(message, args...)
	} else {
		output = message
	}

	// log.Printf 会自动添加时间戳
	log.Printf("%s %s", prefix, output)
}

// Debug 调试日志
func (l *Logger) Debug(message string, args ...interface{}) {
	l.log(DEBUG, message, args...)
}

// Info 信息日志
func (l *Logger) Info(message string, args ...interface{}) {
	l.log(INFO, message, args...)
}

// Warn 警告日志
func (l *Logger) Warn(message string, args ...interface{}) {
	l.log(WARN, message, args...)
}

// Error 错误日志
func (l *Logger) Error(message string, args ...interface{}) {
	l.log(ERROR, message, args...)
}

// Infof 格式化信息日志
func (l *Logger) Infof(format string, args ...interface{}) {
	l.Info(format, args...)
}

// Errorf 格式化错误日志
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.Error(format, args...)
}

// TaskStart 任务开始日志
func (l *Logger) TaskStart(taskID, originalName string) {
	l.Info("task_start task_id=%s name=%s", taskID, originalName)
}

// TaskSuccess 任务成功日志
func (l *Logger) TaskSuccess(taskID string, duration time.Duration, outputSize string) {
	durationStr := formatDuration(duration)
	l.Info("task_success task_id=%s duration=%s output_size=%s", taskID, durationStr, outputSize)
}

// TaskFailed 任务失败日志
func (l *Logger) TaskFailed(taskID, reason string, duration time.Duration) {
	durationStr := formatDuration(duration)
	l.Error("task_failed task_id=%s reason=%s duration=%s", taskID, reason, durationStr)
}

// formatDuration 格式化时长，保留3位小数
func formatDuration(d time.Duration) string {
	totalSeconds := d.Seconds()

	if totalSeconds < 1 {
		// 毫秒级别
		ms := d.Milliseconds()
		return fmt.Sprintf("%dms", ms)
	} else if totalSeconds < 60 {
		// 秒级别
		return fmt.Sprintf("%.3fs", totalSeconds)
	} else if totalSeconds < 3600 {
		// 分钟级别
		minutes := totalSeconds / 60
		return fmt.Sprintf("%.3fm", minutes)
	} else {
		// 小时级别
		hours := totalSeconds / 3600
		return fmt.Sprintf("%.3fh", hours)
	}
}

// init 初始化日志
func init() {
	// 禁用标准 log 的时间前缀，由我们自己控制
	log.SetFlags(log.Ldate | log.Ltime)

	// 如果设置了日志级别环境变量，使用它
	if level := os.Getenv("LOG_LEVEL"); level != "" {
		SetLogLevel(level)
	}
}
