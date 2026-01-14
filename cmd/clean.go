package cmd

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"video-codec-pipeline/internal/config"
	internalredis "video-codec-pipeline/internal/redis"
)

var (
	cleanAll      bool
	cleanPending  bool
	cleanConsumed bool // 新增：清理已消费的消息
	cleanForce    bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "清理任务队列",
	Long: `清理 Redis 中的任务队列。

示例:
  # 查看当前队列状态
  vcp clean -c config.yaml

  # 清理所有未消费的任务（pending）
  vcp clean --pending -c config.yaml

  # 清理已消费的消息（保留最近 100 条）
  vcp clean --consumed -c config.yaml

  # 清理所有任务（包括历史记录）
  vcp clean --all -c config.yaml

  # 强制清理（不需要确认）
  vcp clean --all --force -c config.yaml`,
	Run: runClean,
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanAll, "all", false, "清理所有任务（包括历史）")
	cleanCmd.Flags().BoolVar(&cleanPending, "pending", false, "仅清理未消费的任务")
	cleanCmd.Flags().BoolVar(&cleanConsumed, "consumed", false, "清理已消费的消息（保留最近100条）")
	cleanCmd.Flags().BoolVar(&cleanForce, "force", false, "强制执行，不需要确认")
	cleanCmd.Flags().StringVarP(&configFile, "config", "c", "", "配置文件")
}

func runClean(cmd *cobra.Command, args []string) {
	var cfg *config.Config
	if configFile != "" {
		cfg, _ = config.LoadConfig(configFile)
	}

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
	defer stream.Close()

	if err := stream.Ping(); err != nil {
		log.Fatalf("Redis 连接失败: %v", err)
	}

	// 获取队列信息
	info, err := stream.GetQueueInfo()
	if err != nil {
		log.Fatalf("获取队列信息失败: %v", err)
	}

	// 计算等待消费的任务
	waitingTasks := info.Length - info.Pending
	if waitingTasks < 0 {
		waitingTasks = 0
	}

	fmt.Println()
	fmt.Println("╔════════════════════════════════════════╗")
	fmt.Println("║           任务队列状态                 ║")
	fmt.Println("╠════════════════════════════════════════╣")
	fmt.Printf("║  等待消费:      %-20d  ║\n", waitingTasks)
	fmt.Printf("║  正在处理:      %-20d  ║\n", info.Pending)
	fmt.Printf("║  队列总数:      %-20d  ║\n", info.Length)
	fmt.Printf("║  消费者组数:    %-20d  ║\n", info.Groups)
	fmt.Println("╚════════════════════════════════════════╝")
	fmt.Println()

	// 如果没有指定操作，只显示状态
	if !cleanAll && !cleanPending && !cleanConsumed {
		fmt.Println("操作选项:")
		fmt.Println("  --pending    清理 Pending 任务（正在处理但未完成）")
		fmt.Println("  --consumed   清理队列（保留最近 100 条）")
		fmt.Println("  --all        清理所有任务（包括历史记录）")
		fmt.Println("  --force      强制执行（不确认）")
		fmt.Println()
		fmt.Println("示例:")
		fmt.Println("  vcp clean --consumed -c config.yaml  # 清理已处理的消息")
		fmt.Println("  vcp clean --pending -c config.yaml   # 清理卡住的任务")
		fmt.Println("  vcp clean --all --force -c config.yaml")
		return
	}

	// 确认操作
	if !cleanForce {
		var prompt string
		if cleanAll {
			prompt = "确认清理所有任务和历史记录？此操作不可恢复！(输入 yes 确认): "
		} else if cleanConsumed {
			prompt = "确认清理队列（保留最近 100 条）？(输入 yes 确认): "
		} else {
			prompt = "确认清理所有 Pending 任务？(输入 yes 确认): "
		}
		fmt.Print(prompt)

		reader := bufio.NewReader(os.Stdin)
		confirm, _ := reader.ReadString('\n')
		confirm = strings.TrimSpace(confirm)

		if confirm != "yes" {
			fmt.Println("已取消操作")
			return
		}
	}

	// 执行清理
	if cleanAll {
		fmt.Println("正在清理所有任务...")

		// 删除整个 Stream
		if err := stream.DeleteStream(); err != nil {
			log.Printf("清理 Stream 失败: %v", err)
		} else {
			fmt.Println("  ✓ 已清理任务队列")
		}

		// 删除历史记录
		count, err := stream.DeleteHistory()
		if err != nil {
			log.Printf("清理历史记录失败: %v", err)
		} else {
			fmt.Printf("  ✓ 已清理 %d 条历史记录\n", count)
		}

		fmt.Println()
		fmt.Println("清理完成！")
	} else if cleanConsumed {
		fmt.Println("正在清理队列...")

		// 保留最近 100 条
		trimmed, err := stream.TrimStream(100)
		if err != nil {
			log.Fatalf("清理失败: %v", err)
		}

		fmt.Printf("  ✓ 已清理 %d 条消息（保留最近 100 条）\n", trimmed)
		fmt.Println()
		fmt.Println("清理完成！")
	} else if cleanPending {
		fmt.Println("正在清理 Pending 任务...")

		count, err := stream.CleanPendingTasks(internalredis.DefaultConsumerGroup)
		if err != nil {
			log.Fatalf("清理失败: %v", err)
		}

		fmt.Printf("  ✓ 已清理 %d 个 Pending 任务\n", count)
		fmt.Println()
		fmt.Println("清理完成！")
	}
}
