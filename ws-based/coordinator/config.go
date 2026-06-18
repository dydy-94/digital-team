package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
)

type Config struct {
	ListenAddr         string `json:"listen_addr"`
	MaxHistory         int    `json:"max_history"`
	SpeakerLockTimeout int    `json:"speaker_lock_timeout_ms"`
	SpeakerCooldown    int    `json:"speaker_cooldown_ms"`
	HeartbeatInterval  int    `json:"heartbeat_interval_seconds"`
	ReconnectInterval  int    `json:"reconnect_interval_seconds"`
	InstanceID         string `json:"instance_id"` // 实例唯一ID，自动生成

	// 存储配置
	StorageType string `json:"storage_type"` // "sqlite" or "mysql"
	StoragePath string `json:"storage_path"` // SQLite: 文件路径, MySQL: 忽略
	MySQLHost   string `json:"mysql_host"`
	MySQLPort   int    `json:"mysql_port"`
	MySQLUser   string `json:"mysql_user"`
	MySQLPass   string `json:"mysql_password"`
	MySQLDB     string `json:"mysql_database"`

	// Redis Stream 配置
	RedisEnabled bool   `json:"redis_enabled"`
	RedisHost    string `json:"redis_host"`
	RedisPort    int    `json:"redis_port"`
	RedisPass    string `json:"redis_password"`
	RedisDB      int    `json:"redis_db"`
}

func LoadConfig() (*Config, error) {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to config file")
	flag.Parse()

	// 获取当前可执行文件所在目录
	exePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exeDir := filepath.Dir(exePath)

	config := &Config{
		ListenAddr:         ":8080",
		MaxHistory:         50,
		SpeakerLockTimeout: 2000,
		SpeakerCooldown:    2000,
		HeartbeatInterval:  30,
		ReconnectInterval:  5,
		StorageType:        "sqlite",
		StoragePath:        filepath.Join(exeDir, "data", "coordinator.db"),
		// 默认启用Redis Stream
		RedisEnabled: true,
		RedisHost:    "localhost",
		RedisPort:    6379,
		RedisPass:    "",
		RedisDB:      0,
	}

	if configPath != "" {
		// 将配置文件路径解析为绝对路径
		if !filepath.IsAbs(configPath) {
			// 相对于当前工作目录解析
			configPath, err = filepath.Abs(configPath)
			if err != nil {
				return nil, err
			}
		}

		file, err := os.ReadFile(configPath)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(file, config); err != nil {
			return nil, err
		}

		// 确保数据库路径也是绝对路径
		if config.StoragePath != "" {
			if !filepath.IsAbs(config.StoragePath) {
				if configPath != "" {
					// 如果配置文件中指定了存储路径，并且是相对路径，那么相对于配置文件所在的目录
					configDir := filepath.Dir(configPath)
					config.StoragePath = filepath.Join(configDir, config.StoragePath)
				} else {
					// 如果没有指定配置文件，使用默认路径（相对于可执行文件所在目录）
					config.StoragePath = filepath.Join(exeDir, "data", "coordinator.db")
				}
				// 最终将所有路径转换为绝对路径
				config.StoragePath, err = filepath.Abs(config.StoragePath)
				if err != nil {
					return nil, err
				}
			}
		} else {
			// 如果配置文件中没有指定存储路径，使用默认路径（相对于可执行文件所在目录）
			config.StoragePath = filepath.Join(exeDir, "data", "coordinator.db")
		}
	} else {
		// 如果没有指定配置文件，确保默认路径也是绝对路径
		config.StoragePath, err = filepath.Abs(config.StoragePath)
		if err != nil {
			return nil, err
		}
	}

	// 如果 InstanceID 未配置，使用默认值 "coordinator"
	// 注意：所有实例可以使用相同的 InstanceID，因为消费者位置由 Redis 自动管理
	if config.InstanceID == "" {
		config.InstanceID = "coordinator"
	}

	return config, nil
}
