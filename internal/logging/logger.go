package logging

import (
	"fmt"
	"log"
	"time"
)

// LogLevel 日志级别
type LogLevel string

const (
	DEBUG LogLevel = "DEBUG"
	INFO  LogLevel = "INFO"
	WARN  LogLevel = "WARN"
	ERROR LogLevel = "ERROR"
)

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
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	prefix := fmt.Sprintf("[%s] [%s] [%s]", timestamp, level, l.component)

	if len(args) > 0 {
		log.Printf("%s %s", prefix, fmt.Sprintf(message, args...))
	} else {
		log.Printf("%s %s", prefix, message)
	}
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
	l.Info("task_success task_id=%s duration=%s output_size=%s", taskID, duration, outputSize)
}

// TaskFailed 任务失败日志
func (l *Logger) TaskFailed(taskID, reason string, duration time.Duration) {
	l.Error("task_failed task_id=%s reason=%s duration=%s", taskID, reason, duration)
}
