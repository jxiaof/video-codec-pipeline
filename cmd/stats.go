package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"video-codec-pipeline/internal/config"
	internalredis "video-codec-pipeline/internal/redis"
)

var (
	statsDays int
	statsTask string
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "查询任务统计信息",
	Run:   runStats,
}

func init() {
	statsCmd.Flags().IntVarP(&statsDays, "days", "d", 7, "查询天数")
	statsCmd.Flags().StringVarP(&statsTask, "task", "t", "", "查询指定任务")
	statsCmd.Flags().StringVarP(&configFile, "config", "c", "", "配置文件")
}

func runStats(cmd *cobra.Command, args []string) {
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

	historyMgr := internalredis.NewHistoryManager(stream.Client, 7)

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

		fmt.Println("========== 任务详情 ==========")
		fmt.Printf("任务ID:   %s\n", record.TaskID)
		fmt.Printf("文件:     %s\n", record.FilePath)
		fmt.Printf("状态:     %s\n", record.Status)
		fmt.Printf("开始:     %s\n", record.StartTime.Format(time.RFC3339))
		if !record.EndTime.IsZero() {
			fmt.Printf("结束:     %s\n", record.EndTime.Format(time.RFC3339))
			fmt.Printf("耗时:     %s\n", record.ProcessTime)
		}
		if record.OutputPath != "" {
			fmt.Printf("输出:     %s\n", record.OutputPath)
		}
		if record.ErrorMsg != "" {
			fmt.Printf("错误:     %s\n", record.ErrorMsg)
		}
		return
	}

	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -statsDays)

	stats, err := historyMgr.GetStats(startTime, endTime)
	if err != nil {
		fmt.Printf("获取统计失败: %v\n", err)
		return
	}

	fmt.Println("========== 任务统计 ==========")
	fmt.Printf("周期:     最近 %d 天\n", statsDays)
	fmt.Printf("总数:     %d\n", stats["total"])
	fmt.Printf("完成:     %d\n", stats["completed"])
	fmt.Printf("失败:     %d\n", stats["failed"])
	fmt.Printf("处理中:   %d\n", stats["pending"])
	fmt.Printf("平均耗时: %s\n", stats["avg_duration"])

	records, _ := historyMgr.GetAllHistory(startTime, endTime)
	if len(records) > 0 {
		fmt.Println("\n========== 最近任务 ==========")
		fmt.Printf("%-24s %-10s %-12s %s\n", "任务ID", "状态", "耗时", "文件")
		fmt.Println(strings.Repeat("-", 70))

		for i, r := range records {
			if i >= 15 {
				fmt.Printf("... 共 %d 条\n", len(records))
				break
			}
			id := r.TaskID
			if len(id) > 22 {
				id = id[:22] + ".."
			}
			fmt.Printf("%-24s %-10s %-12s %s\n", id, r.Status, r.ProcessTime, filepath.Base(r.FilePath))
		}
	}
}
