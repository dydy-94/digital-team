package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type Config struct {
	// HTTP 服务
	ListenAddr string `json:"listen_addr"`

	// 数据库
	DBHost     string `json:"db_host"`
	DBPort     int    `json:"db_port"`
	DBUser     string `json:"db_user"`
	DBPassword string `json:"db_password"`
	DBName     string `json:"db_name"`

	// 发言锁配置
	SpeakerLockTimeout int `json:"speaker_lock_timeout_ms"`

	// 心跳超时
	HeartbeatTimeout int `json:"heartbeat_timeout_sec"`

	// 消息保留天数
	MessageRetentionDays int `json:"message_retention_days"`

	// 轮询配置
	PollBatchSize int `json:"poll_batch_size"`
}

var configPath string

func init() {
	flag.StringVar(&configPath, "config", "config.json", "配置文件路径")
}

func LoadConfig() (*Config, error) {
	flag.Parse()

	cfg := &Config{
		ListenAddr: ":8080",

		DBHost:     "localhost",
		DBPort:     3306,
		DBUser:     "root",
		DBPassword: "",
		DBName:     "xclient",

		SpeakerLockTimeout: 2000,
		HeartbeatTimeout:   60,
		MessageRetentionDays: 7,
		PollBatchSize:     50,
	}

	// 1. 尝试从配置文件加载
	data, err := os.ReadFile(configPath)
	if err != nil {
		// log.Printf("[DEBUG] 配置文件读取失败: %v, 使用默认值", err)
	} else {
		// log.Printf("[DEBUG] 配置文件已读取，长度: %d", len(data))
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析配置文件失败: %w", err)
		}
		// log.Printf("[DEBUG] 配置文件解析成功，密码: %s", cfg.DBPassword)
	}

	// 2. 环境变量覆盖（优先级最高）
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("DB_HOST"); v != "" {
		cfg.DBHost = v
	}
	if v := os.Getenv("DB_PORT"); v != "" {
		var port int
		fmt.Sscanf(v, "%d", &port)
		if port > 0 {
			cfg.DBPort = port
		}
	}
	if v := os.Getenv("DB_USER"); v != "" {
		cfg.DBUser = v
	}
	if v := os.Getenv("DB_PASSWORD"); v != "" {
		cfg.DBPassword = v
	}
	if v := os.Getenv("DB_NAME"); v != "" {
		cfg.DBName = v
	}

	return cfg, nil
}

func (c *Config) GetDSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}
