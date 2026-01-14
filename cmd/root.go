package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "vcp",
	Short: "Video Codec Pipeline - 分布式视频编码流水线",
	Long: `VCP (Video Codec Pipeline) 是基于共享存储的分布式视频编码流水线工具。

架构：
  Producer (H800) → 共享存储 (NFS) → Consumer (RTX 5090)
                         ↓
                   Redis Stream (任务队列)

主要功能：
  - producer: 监听目录，将视频复制到共享存储并发布任务
  - consumer: 消费任务，使用 NVENC 硬件编码视频
  - stats:    查询任务历史和统计信息

示例：
  # 启动生产者（H800 端）
  vcp producer --watch /data/videos --shared /mnt/nfs/videos

  # 启动消费者（RTX 5090 端）
  vcp consumer --output /data/encoded

  # 查询统计信息
  vcp stats --days 7`,
}

// Execute 执行根命令
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(producerCmd)
	rootCmd.AddCommand(consumerCmd)
	rootCmd.AddCommand(statsCmd)
}
