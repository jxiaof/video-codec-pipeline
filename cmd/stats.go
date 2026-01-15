package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"video-codec-pipeline/internal/config"
	"video-codec-pipeline/internal/logging"
	internalredis "video-codec-pipeline/internal/redis"
)

var (
	statsDays    int
	showPending  bool
	showConsumer bool
	statsTask    string
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "查询任务统计信息",
	Long: `查询任务统计信息和队列状态。

示例:
  # 查看统计概览
  vcp stats -c config.yaml

  # 查看最近 3 天的统计
  vcp stats -d 3 -c config.yaml

  # 查看指定任务详情
  vcp stats -t task_1234567890 -c config.yaml

  # 查看当前 pending 任务
  vcp stats --pending -c config.yaml

  # 查看消费者信息
  vcp stats --consumers -c config.yaml`,
	Run: runStats,
}

func init() {
	statsCmd.Flags().IntVar(&statsDays, "days", 7, "统计天数")
	statsCmd.Flags().BoolVar(&showPending, "pending", false, "显示 pending 任务")
	statsCmd.Flags().BoolVar(&showConsumer, "consumer", false, "显示消费者信息")
	statsCmd.Flags().StringVar(&statsTask, "task", "", "查询指定任务")
	statsCmd.Flags().StringVarP(&configFile, "config", "c", "", "配置文件")
	statsCmd.Flags().StringVar(&logLevel, "log-level", "info", "日志级别: debug/info/warn/error")
}

func runStats(cmd *cobra.Command, args []string) {
	logging.SetLogLevel(logLevel)

	var cfg *config.Config
	if configFile != "" {
		cfg, _ = config.LoadConfig(configFile)
	}

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
		fmt.Printf("Redis 连接失败: %v\n", err)
		return
	}

	// 显示 pending 任务
	if showPending {
		showPendingTasks(stream)
		return
	}

	// 显示消费者信息
	if showConsumer {
		showConsumerInfo(stream)
		return
	}

	historyMgr := internalredis.NewHistoryManager(stream.Client, 7)

	// 查询单个任务
	if statsTask != "" {
		record, err := historyMgr.GetTaskHistory(statsTask)
		if err != nil {
			fmt.Printf("Error: query failed: %v\n", err)
			return
		}
		if record == nil {
			fmt.Printf("Error: task not found: %s\n", statsTask)
			return
		}

		fmt.Println()
		fmt.Println("=== Task Detail ===")
		fmt.Printf("Task ID:    %s\n", record.TaskID)
		fmt.Printf("File:       %s\n", truncatePathStr(record.FilePath, 48))
		fmt.Printf("Status:     %s\n", record.Status)
		fmt.Printf("Start:      %s\n", record.StartTime.Format("2006-01-02 15:04:05"))
		if !record.EndTime.IsZero() {
			fmt.Printf("End:        %s\n", record.EndTime.Format("2006-01-02 15:04:05"))
			fmt.Printf("Duration:   %s\n", record.ProcessTime)
		}
		if record.OutputPath != "" {
			fmt.Printf("Output:     %s\n", truncatePathStr(record.OutputPath, 48))
		}
		if record.ErrorMsg != "" {
			fmt.Printf("Error:      %s\n", truncateString(record.ErrorMsg, 48))
		}
		fmt.Println()
		return
	}

	// 获取队列状态
	queueInfo, _ := stream.GetQueueInfo()

	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -statsDays)

	stats, err := historyMgr.GetStats(startTime, endTime)
	if err != nil {
		fmt.Printf("Error: get stats failed: %v\n", err)
		return
	}

	// 获取实时的未确认任务
	realTimePending, _ := stream.GetPendingTasks(internalredis.DefaultConsumerGroup, 100)

	fmt.Println()
	fmt.Println("=== VCP Task Statistics ===")
	fmt.Println()
	fmt.Println("[Real-time Queue Status]")
	fmt.Printf("  Queue Length:      %d tasks\n", queueInfo.Length)
	fmt.Printf("  Processing:        %d (unacked by consumer)\n", len(realTimePending))
	fmt.Printf("  Consumer Groups:   %d\n", queueInfo.Groups)
	fmt.Println()
	fmt.Printf("[History Stats] Last %d Days\n", statsDays)
	fmt.Printf("  Total Tasks:   %d\n", stats["total"])
	fmt.Printf("  Completed:     %d\n", stats["completed"])
	fmt.Printf("  Failed:        %d\n", stats["failed"])
	fmt.Printf("  Avg Duration:  %s (只显示已完成任务)\n", stats["avg_duration"])
	fmt.Println()
	fmt.Println("Note: 历史统计依赖于 Consumer 的记录。如果统计为 0 说明任务正在进行，可用 --pending 查看实时任务。")
	fmt.Println()

	// 显示实时的待处理任务
	if len(realTimePending) > 0 {
		fmt.Println("[Currently Processing (Real-time)]")
		for _, p := range realTimePending {
			msgID := p.ID
			if len(msgID) > 25 {
				msgID = msgID[:22] + "..."
			}
			consumer := p.Consumer
			if consumer == "" {
				consumer = "(unassigned)"
			}
			if len(consumer) > 15 {
				consumer = consumer[:12] + "..."
			}
			idle := p.Idle.Round(time.Second).String()
			fmt.Printf("  %s [consumer=%s idle=%s]\n", msgID, consumer, idle)
		}
		fmt.Println()
	}

	records, _ := historyMgr.GetAllHistory(startTime, endTime)
	if len(records) > 0 {
		fmt.Println("=== Recent Tasks ===")
		fmt.Println()
		fmt.Printf("%-20s %-10s %-12s %-15s\n", "ID", "Status", "Duration", "File")
		fmt.Println("---")

		for i, r := range records {
			if i >= 10 {
				fmt.Printf("... and %d more records. Use -t <task_id> to view details\n", len(records)-i)
				break
			}
			id := r.TaskID
			if len(id) > 20 {
				id = id[:17] + "..."
			}
			status := string(r.Status)
			if len(status) > 10 {
				status = status[:10]
			}
			processTime := r.ProcessTime
			if processTime == "" {
				processTime = "-"
			}
			if len(processTime) > 12 {
				processTime = processTime[:12]
			}
			fileName := filepath.Base(r.FilePath)
			if len(fileName) > 15 {
				fileName = fileName[:12] + "..."
			}
			fmt.Printf("%-20s %-10s %-12s %-15s\n", id, status, processTime, fileName)
		}
		fmt.Println()
	}
}

func showPendingTasks(stream *internalredis.Stream) {
	pending, err := stream.GetPendingTasks(internalredis.DefaultConsumerGroup, 50)
	if err != nil {
		fmt.Printf("Error: get pending tasks failed: %v\n", err)
		return
	}

	fmt.Println()
	fmt.Printf("=== Pending Tasks (%d) ===\n", len(pending))
	fmt.Println()

	if len(pending) == 0 {
		fmt.Println("No pending tasks")
		fmt.Println()
		return
	}

	fmt.Printf("%-18s %-16s %-10s %-8s\n", "Message ID", "Consumer", "Idle Time", "Retries")
	fmt.Println("---")

	for _, p := range pending {
		msgID := p.ID
		if len(msgID) > 18 {
			msgID = msgID[:15] + "..."
		}
		consumer := p.Consumer
		if len(consumer) > 16 {
			consumer = consumer[:13] + "..."
		}
		idle := p.Idle.Round(time.Second).String()
		if len(idle) > 10 {
			idle = idle[:10]
		}
		fmt.Printf("%-18s %-16s %-10s %-8d\n", msgID, consumer, idle, p.RetryCount)
	}
	fmt.Println()
}

func showConsumerInfo(stream *internalredis.Stream) {
	// 使用 Stream 的方法获取消费者组信息
	groups, err := stream.GetConsumerGroups()
	if err != nil {
		fmt.Printf("Error: get consumer groups failed: %v\n", err)
		return
	}

	fmt.Println()
	fmt.Printf("=== Consumer Groups (%d) ===\n", len(groups))
	fmt.Println()

	if len(groups) == 0 {
		fmt.Println("No consumer groups")
		fmt.Println()
		return
	}

	for _, g := range groups {
		fmt.Printf("Group: %s\n", g.Name)
		fmt.Printf("  Consumers: %d\n", g.Consumers)
		fmt.Printf("  Pending:   %d\n", g.Pending)
		fmt.Println()

		// 使用 Stream 的方法获取消费者列表
		consumers, err := stream.GetConsumers(g.Name)
		if err != nil {
			continue
		}

		for _, c := range consumers {
			idle := time.Duration(c.Idle) * time.Millisecond
			fmt.Printf("  - %s (Pending: %d, Idle: %s)\n",
				truncateString(c.Name, 20), c.Pending, idle.Round(time.Second).String())
		}
		fmt.Println()
	}
}

// truncatePathStr 截断路径字符串，保留文件名
func truncatePathStr(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	// 保留文件名
	base := filepath.Base(path)
	if len(base) >= maxLen-3 {
		return "..." + base[len(base)-(maxLen-3):]
	}
	remaining := maxLen - len(base) - 4
	if remaining > 0 {
		return path[:remaining] + "..." + "/" + base
	}
	return "..." + base
}

// truncateString 截断字符串（stats专用，避免与其他包冲突）
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
