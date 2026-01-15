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
	statsTask    string
	showPending  bool
	showConsumer bool
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
	statsCmd.Flags().IntVarP(&statsDays, "days", "d", 7, "查询天数")
	statsCmd.Flags().StringVarP(&statsTask, "task", "t", "", "查询指定任务")
	statsCmd.Flags().BoolVar(&showPending, "pending", false, "显示 pending 任务列表")
	statsCmd.Flags().BoolVar(&showConsumer, "consumers", false, "显示消费者信息")
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
			fmt.Printf("查询失败: %v\n", err)
			return
		}
		if record == nil {
			fmt.Printf("任务不存在: %s\n", statsTask)
			return
		}

		fmt.Println()
		fmt.Println("╔════════════════════════════════════════════════════════════╗")
		fmt.Println("║                      任务详情                              ║")
		fmt.Println("╠════════════════════════════════════════════════════════════╣")
		fmt.Printf("║  任务ID:   %-48s║\n", record.TaskID)
		fmt.Printf("║  文件:     %-48s║\n", truncatePathStr(record.FilePath, 48))
		fmt.Printf("║  状态:     %-48s║\n", record.Status)
		fmt.Printf("║  开始:     %-48s║\n", record.StartTime.Format("2006-01-02 15:04:05"))
		if !record.EndTime.IsZero() {
			fmt.Printf("║  结束:     %-48s║\n", record.EndTime.Format("2006-01-02 15:04:05"))
			fmt.Printf("║  耗时:     %-48s║\n", record.ProcessTime)
		}
		if record.OutputPath != "" {
			fmt.Printf("║  输出:     %-48s║\n", truncatePathStr(record.OutputPath, 48))
		}
		if record.ErrorMsg != "" {
			fmt.Printf("║  错误:     %-48s║\n", truncateString(record.ErrorMsg, 48))
		}
		fmt.Println("╚════════════════════════════════════════════════════════════╝")
		return
	}

	// 获取队列状态
	queueInfo, _ := stream.GetQueueInfo()

	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -statsDays)

	stats, err := historyMgr.GetStats(startTime, endTime)
	if err != nil {
		fmt.Printf("获取统计失败: %v\n", err)
		return
	}

	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    VCP 任务统计                            ║")
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Println("║  [队列状态]                                                ║")
	fmt.Printf("║    队列长度:     %-42d║\n", queueInfo.Length)
	fmt.Printf("║    Pending 任务: %-42d║\n", queueInfo.Pending)
	fmt.Printf("║    消费者组:     %-42d║\n", queueInfo.Groups)
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  [历史统计] 最近 %d 天                                      ║\n", statsDays)
	fmt.Printf("║    总任务数:     %-42d║\n", stats["total"])
	fmt.Printf("║    已完成:       %-42d║\n", stats["completed"])
	fmt.Printf("║    失败:         %-42d║\n", stats["failed"])
	fmt.Printf("║    处理中:       %-42d║\n", stats["pending"])
	fmt.Printf("║    平均耗时:     %-42s║\n", stats["avg_duration"])
	fmt.Println("╚════════════════════════════════════════════════════════════╝")

	records, _ := historyMgr.GetAllHistory(startTime, endTime)
	if len(records) > 0 {
		fmt.Println()
		fmt.Println("╔════════════════════════════════════════════════════════════╗")
		fmt.Println("║                    最近任务列表                            ║")
		fmt.Println("╠════════════════════════════════════════════════════════════╣")
		fmt.Println("║  ID                      状态       耗时         文件      ║")
		fmt.Println("╠════════════════════════════════════════════════════════════╣")

		for i, r := range records {
			if i >= 10 {
				fmt.Printf("║  ... 共 %d 条记录，使用 -t <task_id> 查看详情              ║\n", len(records))
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
			fmt.Printf("║  %-20s %-10s %-12s %-15s║\n", id, status, processTime, fileName)
		}
		fmt.Println("╚════════════════════════════════════════════════════════════╝")
	}
}

func showPendingTasks(stream *internalredis.Stream) {
	pending, err := stream.GetPendingTasks(internalredis.DefaultConsumerGroup, 50)
	if err != nil {
		fmt.Printf("获取 pending 任务失败: %v\n", err)
		return
	}

	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Printf("║              Pending 任务列表 (共 %d 个)                    ║\n", len(pending))
	fmt.Println("╠════════════════════════════════════════════════════════════╣")

	if len(pending) == 0 {
		fmt.Println("║  没有 pending 任务                                         ║")
		fmt.Println("╚════════════════════════════════════════════════════════════╝")
		return
	}

	fmt.Println("║  消息ID              消费者            空闲时间    重试次数 ║")
	fmt.Println("╠════════════════════════════════════════════════════════════╣")

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
		fmt.Printf("║  %-18s %-16s %-10s %d          ║\n", msgID, consumer, idle, p.RetryCount)
	}
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
}

func showConsumerInfo(stream *internalredis.Stream) {
	// 使用 Stream 的方法获取消费者组信息
	groups, err := stream.GetConsumerGroups()
	if err != nil {
		fmt.Printf("获取消费者组信息失败: %v\n", err)
		return
	}

	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Printf("║              消费者组信息 (共 %d 个)                        ║\n", len(groups))
	fmt.Println("╠════════════════════════════════════════════════════════════╣")

	if len(groups) == 0 {
		fmt.Println("║  没有消费者组                                              ║")
		fmt.Println("╚════════════════════════════════════════════════════════════╝")
		return
	}

	for _, g := range groups {
		fmt.Printf("║  组名: %-52s║\n", g.Name)
		fmt.Printf("║    消费者数: %-44d║\n", g.Consumers)
		fmt.Printf("║    Pending:  %-44d║\n", g.Pending)
		fmt.Println("╠════════════════════════════════════════════════════════════╣")

		// 使用 Stream 的方法获取消费者列表
		consumers, err := stream.GetConsumers(g.Name)
		if err != nil {
			continue
		}

		for _, c := range consumers {
			idle := time.Duration(c.Idle) * time.Millisecond
			fmt.Printf("║    - %-20s Pending: %-4d 空闲: %-10s║\n",
				truncateString(c.Name, 20), c.Pending, idle.Round(time.Second).String())
		}
	}
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
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
