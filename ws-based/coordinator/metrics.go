package main

import (
	"log"
	"sync"
	"time"
)

// Metrics 定义指标收集器
type Metrics struct {
	wsConnections int64
	speakConflicts int64
	mu sync.Mutex
}

// globalMetrics 全局指标实例
var globalMetrics *Metrics

// InitializeMetrics 初始化指标收集器
func InitializeMetrics() {
	globalMetrics = &Metrics{}
}

// IncWebSocketConnections 增加 WebSocket 连接数
func (m *Metrics) IncWebSocketConnections() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wsConnections++
}

// DecWebSocketConnections 减少 WebSocket 连接数
func (m *Metrics) DecWebSocketConnections() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.wsConnections > 0 {
		m.wsConnections--
	}
}

// IncSpeakConflicts 增加发言冲突数
func (m *Metrics) IncSpeakConflicts() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.speakConflicts++
}

// GetWebSocketConnections 获取当前 WebSocket 连接数
func (m *Metrics) GetWebSocketConnections() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wsConnections
}

// GetSpeakConflicts 获取当前发言冲突数
func (m *Metrics) GetSpeakConflicts() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.speakConflicts
}

// StartMetricsResetTicker 启动定时重置指标的 ticker
func StartMetricsResetTicker() {
	ticker := time.NewTicker(24 * time.Hour) // 每天重置一次
	defer ticker.Stop()

	for range ticker.C {
		InitializeMetrics() // 重置指标
		log.Println("Metrics reset")
	}
}
