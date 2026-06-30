package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// PermissionCache 权限缓存
type PermissionCache struct {
	coordinatorURL string
	httpClient     *http.Client
	cache          map[string]*CachedPermission
	mu             sync.RWMutex
	cacheExpiry    time.Duration
}

// CachedPermission 缓存的权限
type CachedPermission struct {
	AgentID      string    `json:"agent_id"`
	Level        string    `json:"level"`
	AllowedTools []string  `json:"allowed_tools"`
	DeniedTools  []string  `json:"denied_tools"`
	CachedAt     time.Time `json:"cached_at"`
}

// NewPermissionCache 创建权限缓存
func NewPermissionCache(coordinatorURL string) *PermissionCache {
	return &PermissionCache{
		coordinatorURL: coordinatorURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		cache:       make(map[string]*CachedPermission),
		cacheExpiry: 5 * time.Minute, // 缓存5分钟
	}
}

// GetPermission 获取权限（优先从缓存，缓存过期则从 Coordinator 获取）
func (p *PermissionCache) GetPermission(agentID string) (*CachedPermission, error) {
	// 先检查缓存
	p.mu.RLock()
	if cached, exists := p.cache[agentID]; exists {
		if time.Since(cached.CachedAt) < p.cacheExpiry {
			p.mu.RUnlock()
			return cached, nil
		}
	}
	p.mu.RUnlock()

	// 缓存过期或不存在，从 Coordinator 获取
	if err := p.refreshPermission(agentID); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cache[agentID], nil
}

// refreshPermission 从 Coordinator 刷新权限
func (p *PermissionCache) refreshPermission(agentID string) error {
	resp, err := p.httpClient.Get(fmt.Sprintf("%s/api/agent/%s/permission", p.coordinatorURL, agentID))
	if err != nil {
		return fmt.Errorf("获取权限失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			// 没有权限记录，缓存一个空记录
			p.mu.Lock()
			p.cache[agentID] = &CachedPermission{
				AgentID:  agentID,
				Level:    "l1", // 默认级别
				CachedAt: time.Now(),
			}
			p.mu.Unlock()
			return nil
		}
		return fmt.Errorf("获取权限失败，状态码: %d", resp.StatusCode)
	}

	var perm AgentPermissionResponse
	if err := json.NewDecoder(resp.Body).Decode(&perm); err != nil {
		return fmt.Errorf("解析权限响应失败: %w", err)
	}

	// 解析 allowed_tools 和 denied_tools
	var allowedTools, deniedTools []string
	if perm.AllowedTools != "" && perm.AllowedTools != "[]" {
		json.Unmarshal([]byte(perm.AllowedTools), &allowedTools)
	}
	if perm.DeniedTools != "" && perm.DeniedTools != "[]" {
		json.Unmarshal([]byte(perm.DeniedTools), &deniedTools)
	}

	p.mu.Lock()
	p.cache[agentID] = &CachedPermission{
		AgentID:      agentID,
		Level:        perm.Level,
		AllowedTools: allowedTools,
		DeniedTools:  deniedTools,
		CachedAt:     time.Now(),
	}
	p.mu.Unlock()

	return nil
}

// CheckPermission 检查权限
// 返回 allowed=true 表示允许执行，allowed=false 表示拒绝
func (p *PermissionCache) CheckPermission(agentID, tool, action string) (bool, string) {
	perm, err := p.GetPermission(agentID)
	if err != nil {
		log.Printf("[WARN] [PermissionCache] 检查权限失败，默认为允许: agent=%s, error=%v", agentID, err)
		return true, "error_default_allow"
	}

	// 检查是否在 denied_tools 中
	for _, t := range perm.DeniedTools {
		if t == tool || t == "*" {
			return false, "in_denied_tools"
		}
	}

	// 如果 allowed_tools 非空，检查是否在列表中
	if len(perm.AllowedTools) > 0 {
		found := false
		for _, t := range perm.AllowedTools {
			if t == tool || t == "*" {
				found = true
				break
			}
		}
		if !found {
			return false, "not_in_allowed_tools"
		}
	}

	return true, "allowed"
}

// StartCleanupRoutine 启动后台缓存清理 goroutine
func (p *PermissionCache) StartCleanupRoutine(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			p.cleanupExpired()
		}
	}()
}

// cleanupExpired 清理过期缓存
func (p *PermissionCache) cleanupExpired() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for agentID, cached := range p.cache {
		if now.Sub(cached.CachedAt) > p.cacheExpiry {
			delete(p.cache, agentID)
		}
	}

	if len(p.cache) > 0 {
		log.Printf("[DEBUG] [PermissionCache] 缓存清理完成，当前缓存数量: %d", len(p.cache))
	}
}

// InvalidateCache 使缓存失效
func (p *PermissionCache) InvalidateCache(agentID string) {
	p.mu.Lock()
	delete(p.cache, agentID)
	p.mu.Unlock()
}

// ClearCache 清除所有缓存
func (p *PermissionCache) ClearCache() {
	p.mu.Lock()
	p.cache = make(map[string]*CachedPermission)
	p.mu.Unlock()
}

// AgentPermissionResponse Coordinator 返回的权限响应
type AgentPermissionResponse struct {
	AgentID             string `json:"agent_id"`
	Level               string `json:"level"`
	AllowedTools        string `json:"allowed_tools"`
	DeniedTools         string `json:"denied_tools"`
	DailyTokenLimit     int64  `json:"daily_token_limit"`
	MonthlyTokenLimit   int64  `json:"monthly_token_limit"`
	FileSizeLimitMB     int    `json:"file_size_limit_mb"`
	MessageLimitPerHour int    `json:"message_limit_per_hour"`
}
