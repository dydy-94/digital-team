package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
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

	// 心跳超时
	HeartbeatTimeout int `json:"heartbeat_timeout_sec"`

	// 消息保留天数
	MessageRetentionDays int `json:"message_retention_days"`

	// 轮询配置
	PollBatchSize int `json:"poll_batch_size"`

	// 超时配置（毫秒）
	CleanupIntervalMs      int `json:"cleanup_interval_ms"`       // 连接清理间隔
	PingTimeoutMs          int `json:"ping_timeout_ms"`           // Ping超时
	PollIntervalMs         int `json:"poll_interval_ms"`          // 消息轮询间隔
	MemberStatusIntervalMs int `json:"member_status_interval_ms"` // 成员状态推送间隔
	MessageSendTimeoutMs   int `json:"message_send_timeout_ms"`   // 消息发送超时

	// 日志级别配置
	LogLevel string `json:"log_level"` // debug, info, warn, error

	// S3 配置
	S3 S3Config `json:"s3"`
}

// S3Config S3 配置
type S3Config struct {
	Bucket           string `json:"bucket"`
	Region           string `json:"region"`
	AccessKeyID      string `json:"access_key_id"`
	SecretAccessKey  string `json:"secret_access_key"`
	Endpoint         string `json:"endpoint"`           // 兼容 MinIO 等自建 S3
	PresignExpiryMin int    `json:"presign_expiry_min"` // Presigned URL 过期时间（分钟）
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

		HeartbeatTimeout:     60,
		MessageRetentionDays: 7,
		PollBatchSize:        50,

		// 超时配置默认值（毫秒）
		CleanupIntervalMs:      300000, // 5分钟（清理任务间隔）
		PingTimeoutMs:          100,    // 100毫秒
		PollIntervalMs:         500,    // 500毫秒
		MemberStatusIntervalMs: 30000,  // 30秒
		MessageSendTimeoutMs:   2000,   // 2秒

		// 日志级别配置
		LogLevel: "info", // 默认 info
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

	// S3 环境变量覆盖
	if v := os.Getenv("S3_BUCKET"); v != "" {
		cfg.S3.Bucket = v
	}
	if v := os.Getenv("S3_REGION"); v != "" {
		cfg.S3.Region = v
	}
	if v := os.Getenv("S3_ACCESS_KEY_ID"); v != "" {
		cfg.S3.AccessKeyID = v
	}
	if v := os.Getenv("S3_SECRET_ACCESS_KEY"); v != "" {
		cfg.S3.SecretAccessKey = v
	}
	if v := os.Getenv("S3_ENDPOINT"); v != "" {
		cfg.S3.Endpoint = v
	}

	return cfg, nil
}

func (c *Config) GetDSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}

// 超时配置辅助方法
func (c *Config) GetCleanupInterval() time.Duration {
	if c.CleanupIntervalMs <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.CleanupIntervalMs) * time.Millisecond
}

func (c *Config) GetPingTimeout() time.Duration {
	if c.PingTimeoutMs <= 0 {
		return 100 * time.Millisecond
	}
	return time.Duration(c.PingTimeoutMs) * time.Millisecond
}

func (c *Config) GetPollInterval() time.Duration {
	if c.PollIntervalMs <= 0 {
		return 500 * time.Millisecond
	}
	return time.Duration(c.PollIntervalMs) * time.Millisecond
}

func (c *Config) GetMemberStatusInterval() time.Duration {
	if c.MemberStatusIntervalMs <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.MemberStatusIntervalMs) * time.Millisecond
}

func (c *Config) GetMessageSendTimeout() time.Duration {
	if c.MessageSendTimeoutMs <= 0 {
		return 2 * time.Second
	}
	return time.Duration(c.MessageSendTimeoutMs) * time.Millisecond
}

// GetPresignExpiry 获取 Presigned URL 过期时间
func (c *Config) GetPresignExpiry() time.Duration {
	if c.S3.PresignExpiryMin <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(c.S3.PresignExpiryMin) * time.Minute
}

// IsS3Configured 检查 S3 是否已配置
func (c *Config) IsS3Configured() bool {
	return c.S3.Bucket != "" && c.S3.Region != "" && c.S3.AccessKeyID != "" && c.S3.SecretAccessKey != ""
}
