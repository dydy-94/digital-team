package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type Config struct {
	AgentID        string `json:"agent_id"`
	CoordinatorURL string `json:"coordinator_url"` // HTTP URL，不是 WS
	AgentCoreURL   string `json:"agentcore_url"`
	ListenAddr     string `json:"listen_addr"`
	Endpoint       string `json:"endpoint"` // x-client 暴露给 coordinator 调用的地址

	// 轮询配置
	PollInterval  int `json:"poll_interval"`   // 轮询间隔（秒），默认 5
	PollBatchSize int `json:"poll_batch_size"` // 每次轮询获取的消息数

	// AgentCore 配置
	HeartbeatInterval int `json:"heartbeat_interval"` // 心跳间隔（秒）

	// 上下文管理
	MaxMemorySize  int `json:"max_memory_size"`  // 最大消息数
	MaxMemoryChars int `json:"max_memory_chars"` // 最大字符数

	// Agent 模板配置
	Template TemplateConfig `json:"template"`
}

// TemplateConfig 模板配置
type TemplateConfig struct {
	Enabled  bool   `json:"enabled"`             // 是否启用模板
	Source   string `json:"source"`              // 模板来源: "coordinator" (默认) 或 "file"
	FileDir  string `json:"file_dir,omitempty"`  // 当 source="file" 时使用的本地目录
	SoulMode string `json:"soul_mode,omitempty"` // Soul注入模式: "always" (默认) 或 "once"
}

var configPath string

func init() {
	flag.StringVar(&configPath, "config", "config.json", "配置文件路径")
}

func LoadConfig() (*Config, error) {

	cfg := &Config{
		PollInterval:      5,
		PollBatchSize:     50,
		HeartbeatInterval: 30,
		MaxMemorySize:     50,
		MaxMemoryChars:    2000,
	}

	// 如果配置文件存在，读取它
	if data, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析配置文件失败: %w", err)
		}
	}

	// 从环境变量覆盖
	if v := os.Getenv("AGENT_ID"); v != "" {
		cfg.AgentID = v
	}
	if v := os.Getenv("COORDINATOR_URL"); v != "" {
		cfg.CoordinatorURL = v
	}
	if v := os.Getenv("AGENTCORE_URL"); v != "" {
		cfg.AgentCoreURL = v
	}
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}

	// 验证必需配置
	if cfg.AgentID == "" {
		return nil, fmt.Errorf("agent_id 不能为空")
	}
	if cfg.CoordinatorURL == "" {
		return nil, fmt.Errorf("coordinator_url 不能为空")
	}
	if cfg.AgentCoreURL == "" {
		return nil, fmt.Errorf("agentcore_url 不能为空")
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8001"
	}

	// 设置默认值
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5
	}
	if cfg.PollBatchSize <= 0 {
		cfg.PollBatchSize = 50
	}

	// 模板默认值
	if cfg.Template.Source == "" {
		cfg.Template.Source = "coordinator" // 默认从 Coordinator 获取
	}
	if cfg.Template.Source == "file" && cfg.Template.FileDir == "" {
		homeDir, _ := os.UserHomeDir()
		cfg.Template.FileDir = fmt.Sprintf("%s/.x-client/templates/%s", homeDir, cfg.AgentID)
	}
	if cfg.Template.SoulMode == "" {
		cfg.Template.SoulMode = "always" // 默认每次对话都注入Soul Context
	}

	return cfg, nil
}
