package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config 定义了应用程序的配置结构
type Config struct {
	Redis    RedisConfig    `yaml:"redis"`
	Producer ProducerConfig `yaml:"producer"`
	Consumer ConsumerConfig `yaml:"consumer"`
}

// RedisConfig 包含 Redis 相关配置
type RedisConfig struct {
	Address  string `yaml:"address"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// ProducerConfig 包含 Producer 相关配置
type ProducerConfig struct {
	WatchDirectory  string `yaml:"watch_directory"`  // 监听目录
	SharedDirectory string `yaml:"shared_directory"` // 共享存储目录
	OutputDirectory string `yaml:"output_directory"` // Consumer 输出目录
	OutputPrefix    string `yaml:"output_prefix"`    // 输出文件名前缀
	FFmpegPreset    string `yaml:"ffmpeg_preset"`    // FFmpeg 预设
	FFmpegArgs      string `yaml:"ffmpeg_args"`      // 自定义 FFmpeg 参数
	VerifyOutput    bool   `yaml:"verify_output"`    // 是否校验
	WatchMode       string `yaml:"watch_mode"`       // new / all
	KeepLocal       bool   `yaml:"keep_local"`       // 是否保留本地原文件
}

// ConsumerConfig 包含 Consumer 相关配置
type ConsumerConfig struct {
	Name        string `yaml:"name"`
	Concurrency int    `yaml:"concurrency"` // 并发处理数（默认 1）
}

// LoadConfig 从指定的 YAML 文件加载配置
func LoadConfig(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	// 默认值
	if config.Redis.Address == "" {
		config.Redis.Address = "localhost:6379"
	}
	if config.Consumer.Concurrency == 0 {
		config.Consumer.Concurrency = 1
	}
	if config.Producer.WatchMode == "" {
		config.Producer.WatchMode = "new"
	}
	if config.Producer.FFmpegPreset == "" {
		config.Producer.FFmpegPreset = "h264-nvenc"
	}

	return &config, nil
}

// GetRedisAddr 获取 Redis 地址
func (c *Config) GetRedisAddr() string {
	return c.Redis.Address
}
