package cmd

// 共享的命令行变量
var (
	configFile string // 配置文件路径，被多个命令使用
	logLevel   string // 新增：日志级别
)
