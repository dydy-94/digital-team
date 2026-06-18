package main

import (
	"encoding/json"
	"flag"
	"os"
)

type Config struct {
	AgentID           string `json:"agent_id"`
	ChannelID         string `json:"channel_id"`
	CoordinatorURL    string `json:"coordinator_url"`
	AgentCoreURL      string `json:"agent_core_url"`
	ListenAddr        string `json:"listen_addr"`
	Endpoint          string `json:"endpoint"`           // Agent 的公网访问地址（用于 coordinator 调用）
	MaxMemorySize     int    `json:"max_memory_size"`
	MaxMemoryChars    int    `json:"max_memory_chars"`
	HeartbeatInterval int    `json:"heartbeat_interval_seconds"`
	ReconnectInterval int    `json:"reconnect_interval_seconds"`
}

func LoadConfig() (*Config, error) {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to config file")

	var cfg Config
	flag.StringVar(&cfg.AgentID, "agent-id", "agent_default", "Agent ID")
	flag.StringVar(&cfg.ChannelID, "channel-id", "default_channel", "Channel ID")
	flag.StringVar(&cfg.CoordinatorURL, "coordinator", "ws://localhost:8080", "Coordinator URL")
	flag.StringVar(&cfg.AgentCoreURL, "agentcore", "http://localhost:8000", "AgentCore URL")
	flag.StringVar(&cfg.ListenAddr, "listen", ":8081", "HTTP listen address")
	flag.StringVar(&cfg.Endpoint, "endpoint", "", "Agent's public endpoint URL for coordinator to call")
	flag.IntVar(&cfg.MaxMemorySize, "max-memory-size", 50, "Max memory window size")
	flag.IntVar(&cfg.MaxMemoryChars, "max-memory-chars", 10000, "Max memory chars")
	flag.IntVar(&cfg.HeartbeatInterval, "heartbeat-interval", 30, "Heartbeat interval in seconds")
	flag.IntVar(&cfg.ReconnectInterval, "reconnect-interval", 5, "Reconnect interval in seconds")

	flag.Parse()

	if configPath != "" {
		file, err := os.ReadFile(configPath)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(file, &cfg); err != nil {
			return nil, err
		}
	}

	return &cfg, nil
}
