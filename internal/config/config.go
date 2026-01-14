package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config 定义了应用程序的配置结构
type Config struct {
	Redis    RedisConfig       `yaml:"redis"`
	Producer ProducerConfig    `yaml:"producer"`
	Consumer ConsumerConfig    `yaml:"consumer"`
	Presets  map[string]string `yaml:"presets"` // 自定义预设模板
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
	FFmpegPreset    string `yaml:"ffmpeg_preset"`    // FFmpeg 预设名称
	FFmpegArgs      string `yaml:"ffmpeg_args"`      // 自定义 FFmpeg 参数（覆盖预设）
	VerifyOutput    bool   `yaml:"verify_output"`    // 是否校验
	WatchMode       string `yaml:"watch_mode"`       // new / all
	KeepLocal       bool   `yaml:"keep_local"`       // 是否保留本地原文件
}

// ConsumerConfig 包含 Consumer 相关配置
type ConsumerConfig struct {
	Name        string `yaml:"name"`
	Concurrency int    `yaml:"concurrency"`
}

// 内置预设模板
var builtinPresets = map[string]string{
	"h264-nvenc":    "-c:v h264_nvenc -preset p4 -b:v 10M -c:a aac -b:a 128k -movflags +faststart",
	"h264-nvenc-hq": "-c:v h264_nvenc -preset p7 -tune hq -b:v 15M -maxrate 20M -bufsize 30M -c:a aac -b:a 192k -movflags +faststart",
	"h265-nvenc":    "-c:v hevc_nvenc -preset p4 -b:v 8M -c:a aac -b:a 128k -movflags +faststart",
	"h265-nvenc-hq": "-c:v hevc_nvenc -preset p7 -tune hq -b:v 10M -c:a aac -b:a 192k -movflags +faststart",
	"h264-cpu":      "-c:v libx264 -preset medium -crf 23 -c:a aac -b:a 128k -movflags +faststart",
	"h265-cpu":      "-c:v libx265 -preset medium -crf 28 -c:a aac -b:a 128k -movflags +faststart",
	"copy":          "-c copy",
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

// GetPreset 获取预设参数（优先配置文件中的自定义预设，其次内置预设）
func (c *Config) GetPreset(name string) (string, bool) {
	// 优先查找配置文件中的自定义预设
	if c != nil && c.Presets != nil {
		if args, ok := c.Presets[name]; ok {
			return args, true
		}
	}
	// 其次查找内置预设
	if args, ok := builtinPresets[name]; ok {
		return args, true
	}
	return "", false
}

// GetAllPresets 获取所有可用预设（合并内置和自定义）
func (c *Config) GetAllPresets() map[string]string {
	result := make(map[string]string)
	// 先复制内置预设
	for k, v := range builtinPresets {
		result[k] = v
	}
	// 再用配置文件中的覆盖（允许重写内置预设）
	if c != nil && c.Presets != nil {
		for k, v := range c.Presets {
			result[k] = v
		}
	}
	return result
}

// GetBuiltinPresets 获取内置预设（用于帮助信息）
func GetBuiltinPresets() map[string]string {
	return builtinPresets
}
