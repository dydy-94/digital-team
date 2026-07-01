package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// 触发器状态常量
const (
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"
	StatusInvalid  = "invalid"
	StatusExpired  = "expired"
)

// CoordinatorClient Coordinator API 客户端
type CoordinatorClient struct {
	url        string
	agentID    string
	httpClient *http.Client
}

// NewCoordinatorClient 创建 Coordinator 客户端
func NewCoordinatorClient(url, agentID string) *CoordinatorClient {
	return &CoordinatorClient{
		url:     url,
		agentID: agentID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetTriggers 获取 X-Client 的所有触发器
func (c *CoordinatorClient) GetTriggers() (*TriggerListResponse, error) {
	url := fmt.Sprintf("%s/api/triggers?xclient_id=%s", c.url, c.agentID)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result TriggerListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetTrigger 获取单个触发器
func (c *CoordinatorClient) GetTrigger(triggerID string) (*Trigger, error) {
	url := fmt.Sprintf("%s/api/trigger/%s", c.url, triggerID)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var trigger Trigger
	if err := json.Unmarshal(body, &trigger); err != nil {
		return nil, err
	}
	return &trigger, nil
}

// CreateTrigger 创建触发器
func (c *CoordinatorClient) CreateTrigger(req *CreateTriggerRequest) (*Trigger, error) {
	url := fmt.Sprintf("%s/api/trigger", c.url)

	body, _ := json.Marshal(req)
	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("创建触发器失败: %s", string(respBody))
	}

	var result TriggerResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	// 获取创建的触发器
	return c.GetTrigger(result.TriggerID)
}

// UpdateTrigger 更新触发器
func (c *CoordinatorClient) UpdateTrigger(triggerID string, req *UpdateTriggerRequest) error {
	url := fmt.Sprintf("%s/api/trigger/%s", c.url, triggerID)

	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("更新触发器失败: %s", string(respBody))
	}

	return nil
}

// DeleteTrigger 删除触发器
func (c *CoordinatorClient) DeleteTrigger(triggerID string) error {
	url := fmt.Sprintf("%s/api/trigger/%s", c.url, triggerID)

	httpReq, _ := http.NewRequest(http.MethodDelete, url, nil)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// NotifyTrigger 触发器触发通知
func (c *CoordinatorClient) NotifyTrigger(trigger *Trigger) error {
	url := fmt.Sprintf("%s/api/trigger/notify", c.url)

	req := &TriggerNotifyRequest{
		TriggerID:   trigger.ID,
		XClientID:   trigger.XClientID,
		TriggerType: trigger.Type,
		Reason:      trigger.Reason,
	}

	body, _ := json.Marshal(req)
	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("触发通知失败: %s", string(respBody))
	}

	return nil
}

// TriggerRuntime 触发器运行时
type TriggerRuntime struct {
	mu          sync.RWMutex
	triggers    map[string]*Trigger // trigger_id -> Trigger
	cronJobs    map[string]int64    // trigger_id -> cron entry id (使用 int64)
	cronStop    chan struct{}
	intervals   map[string]*time.Ticker // trigger_id -> interval ticker
	xclientID   string
	endpoint    string
	coordinator *CoordinatorClient
	stopChan    chan struct{}
}

// NewTriggerRuntime 创建触发器运行时
func NewTriggerRuntime(xclientID, endpoint string, coordinator *CoordinatorClient) *TriggerRuntime {
	return &TriggerRuntime{
		triggers:    make(map[string]*Trigger),
		cronJobs:    make(map[string]int64),
		cronStop:    make(chan struct{}),
		intervals:   make(map[string]*time.Ticker),
		xclientID:   xclientID,
		endpoint:    endpoint,
		coordinator: coordinator,
		stopChan:    make(chan struct{}),
	}
}

// Start 启动触发器运行时
func (r *TriggerRuntime) Start(ctx context.Context) error {
	// 从 Coordinator 加载触发器
	triggers, err := r.loadTriggers()
	if err != nil {
		log.Printf("Failed to load triggers: %v", err)
	}

	for _, t := range triggers {
		if err := r.registerTrigger(t); err != nil {
			log.Printf("Failed to register trigger %s: %v", t.ID, err)
		}
	}

	// 启动同步循环
	go r.syncLoop(ctx)

	log.Printf("TriggerRuntime started with %d triggers", len(triggers))
	return nil
}

// Stop 停止触发器运行时
func (r *TriggerRuntime) Stop() {
	close(r.stopChan)
	for _, ticker := range r.intervals {
		ticker.Stop()
	}
	log.Println("TriggerRuntime stopped")
}

// loadTriggers 从 Coordinator 加载触发器
func (r *TriggerRuntime) loadTriggers() ([]*Trigger, error) {
	resp, err := r.coordinator.GetTriggers()
	if err != nil {
		return nil, err
	}
	return resp.Triggers, nil
}

// registerTrigger 注册触发器到运行时
func (r *TriggerRuntime) registerTrigger(t *Trigger) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 检查状态
	if t.Status != StatusEnabled || !t.RoomValid {
		log.Printf("Skipping trigger %s: status=%s, room_valid=%v", t.ID, t.Status, t.RoomValid)
		return nil
	}

	r.triggers[t.ID] = t

	switch t.Type {
	case "cron":
		return r.registerCron(t)
	case "interval":
		return r.registerInterval(t)
	case "once":
		return r.registerOnce(t)
	case "poll":
		return r.registerPoll(t)
	case "webhook":
		// Webhook 由外部调用触发，无需注册到调度器
	case "on_message":
		// 消息事件由消息处理器处理
	default:
		log.Printf("Unknown trigger type: %s", t.Type)
	}

	return nil
}

// unregisterTrigger 从运行时移除触发器
func (r *TriggerRuntime) unregisterTrigger(triggerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.triggers[triggerID]; ok {
		delete(r.triggers, triggerID)
		delete(r.cronJobs, triggerID)
		if ticker, ok := r.intervals[triggerID]; ok {
			ticker.Stop()
			delete(r.intervals, triggerID)
		}
		log.Printf("Unregistered trigger: %s", triggerID)
	}
}

// registerCron 注册 Cron 触发器（简化版，使用 time.Ticker 模拟）
func (r *TriggerRuntime) registerCron(t *Trigger) error {
	var cfg struct {
		Expr string `json:"expr"`
	}
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return err
	}

	// 简化实现：每分钟检查一次
	// 实际应该使用 cron 库，但为了减少依赖，这里用简化的方式
	ticker := time.NewTicker(time.Minute)
	r.intervals[t.ID] = ticker

	triggerID := t.ID
	go func() {
		for {
			select {
			case <-ticker.C:
				r.checkCronAndFire(triggerID)
			case <-r.stopChan:
				return
			}
		}
	}()

	log.Printf("Registered cron trigger: %s (%s)", t.ID, t.Name)
	return nil
}

// checkCronAndFire 检查 Cron 是否应该触发
func (r *TriggerRuntime) checkCronAndFire(triggerID string) {
	r.mu.RLock()
	t, ok := r.triggers[triggerID]
	r.mu.RUnlock()

	if !ok {
		return
	}

	// 简化实现：每分钟都触发
	// 实际应该使用 croniter 库来正确解析 Cron 表达式并检查是否应该触发
	_ = t // 避免编译警告
	r.fireTrigger(triggerID)
}

// registerInterval 注册间隔触发器
func (r *TriggerRuntime) registerInterval(t *Trigger) error {
	var cfg struct {
		Minutes int `json:"minutes"`
		Seconds int `json:"seconds"`
	}
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return err
	}

	interval := time.Duration(cfg.Minutes)*time.Minute + time.Duration(cfg.Seconds)*time.Second
	if interval == 0 {
		interval = time.Minute
	}

	ticker := time.NewTicker(interval)
	r.intervals[t.ID] = ticker

	triggerID := t.ID
	go func() {
		for {
			select {
			case <-ticker.C:
				r.fireTrigger(triggerID)
			case <-r.stopChan:
				return
			}
		}
	}()

	log.Printf("Registered interval trigger: %s (%s) every %v", t.ID, t.Name, interval)
	return nil
}

// registerOnce 注册单次触发器
func (r *TriggerRuntime) registerOnce(t *Trigger) error {
	var cfg struct {
		At string `json:"at"` // ISO8601 时间格式
	}
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return err
	}

	at, err := time.Parse(time.RFC3339, cfg.At)
	if err != nil {
		return err
	}

	// 计算延迟
	delay := time.Until(at)
	if delay <= 0 {
		// 已经过期，立即触发
		go r.fireTrigger(t.ID)
		return nil
	}

	triggerID := t.ID
	go func() {
		select {
		case <-time.After(delay):
			r.fireTrigger(triggerID)
		case <-r.stopChan:
			return
		}
	}()

	log.Printf("Registered once trigger: %s (%s) at %s", t.ID, t.Name, at.Format(time.RFC3339))
	return nil
}

// registerPoll 注册轮询触发器
func (r *TriggerRuntime) registerPoll(t *Trigger) error {
	var cfg struct {
		URL         string `json:"url"`
		JSONPath    string `json:"json_path"`
		CompareVal  string `json:"compare_value"`
		IntervalMin int    `json:"interval_minutes"`
	}
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return err
	}

	interval := time.Duration(cfg.IntervalMin) * time.Minute
	if interval == 0 {
		interval = 5 * time.Minute
	}

	ticker := time.NewTicker(interval)
	r.intervals[t.ID] = ticker

	triggerID := t.ID
	go func() {
		for {
			select {
			case <-ticker.C:
				r.checkPoll(triggerID)
			case <-r.stopChan:
				return
			}
		}
	}()

	log.Printf("Registered poll trigger: %s (%s) every %v", t.ID, t.Name, interval)
	return nil
}

// fireTrigger 触发触发器
func (r *TriggerRuntime) fireTrigger(triggerID string) {
	r.mu.RLock()
	t, ok := r.triggers[triggerID]
	r.mu.RUnlock()

	if !ok {
		log.Printf("Trigger %s not found, skipping", triggerID)
		return
	}

	// 冷却检查
	if t.CooldownSeconds > 0 && t.LastFiredAt > 0 {
		elapsed := time.Now().UnixMilli() - t.LastFiredAt
		if elapsed < int64(t.CooldownSeconds*1000) {
			log.Printf("Trigger %s in cooldown (%dms elapsed)", t.ID, elapsed)
			return
		}
	}

	// 最大触发次数检查
	if t.MaxFires != nil && t.FireCount >= *t.MaxFires {
		r.mu.Lock()
		t.Status = StatusExpired
		r.unregisterTrigger(triggerID)
		r.mu.Unlock()
		log.Printf("Trigger %s expired (max fires reached)", t.ID)
		return
	}

	// 更新状态
	r.mu.Lock()
	t.FireCount++
	t.LastFiredAt = time.Now().UnixMilli()
	r.mu.Unlock()

	// 通知 Coordinator
	if err := r.coordinator.NotifyTrigger(t); err != nil {
		log.Printf("Failed to notify trigger %s: %v", t.ID, err)
	} else {
		log.Printf("Trigger fired: %s (%s)", t.ID, t.Name)
	}
}

// checkPoll 检查轮询触发器
func (r *TriggerRuntime) checkPoll(triggerID string) {
	// 简化实现：直接触发
	// 实际应该 HTTP GET URL，提取值，与 CompareVal 比较
	r.fireTrigger(triggerID)
}

// syncLoop 同步循环
func (r *TriggerRuntime) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopChan:
			return
		case <-ticker.C:
			r.checkTriggers()
		}
	}
}

// checkTriggers 检查触发器状态
func (r *TriggerRuntime) checkTriggers() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for id, t := range r.triggers {
		if t.Status == StatusEnabled && t.RoomValid {
			// 检查是否过期
			if t.MaxFires != nil && t.FireCount >= *t.MaxFires {
				log.Printf("Trigger %s reached max fires, expiring", id)
			}
		}
	}
}

// CreateTrigger 创建触发器
func (r *TriggerRuntime) CreateTrigger(req *CreateTriggerRequest) (*Trigger, error) {
	// 通过 Coordinator API 创建
	trigger, err := r.coordinator.CreateTrigger(req)
	if err != nil {
		return nil, err
	}

	// 注册到运行时
	if err := r.registerTrigger(trigger); err != nil {
		return nil, err
	}

	return trigger, nil
}

// UpdateTrigger 更新触发器
func (r *TriggerRuntime) UpdateTrigger(triggerID string, req *UpdateTriggerRequest) error {
	// 通过 Coordinator API 更新
	if err := r.coordinator.UpdateTrigger(triggerID, req); err != nil {
		return err
	}

	// 如果更新了 room_id 或状态，需要重新注册
	if req.RoomID != nil || req.IsEnabled != nil {
		// 重新加载触发器
		trigger, err := r.coordinator.GetTrigger(triggerID)
		if err != nil {
			return err
		}

		// 先移除旧的
		r.unregisterTrigger(triggerID)

		// 重新注册
		if err := r.registerTrigger(trigger); err != nil {
			return err
		}
	}

	return nil
}

// DeleteTrigger 删除触发器
func (r *TriggerRuntime) DeleteTrigger(triggerID string) error {
	// 从运行时移除
	r.unregisterTrigger(triggerID)

	// 通过 Coordinator API 删除
	return r.coordinator.DeleteTrigger(triggerID)
}

// InvalidateTriggersByRoom 使聊天室关联的所有触发器失效
func (r *TriggerRuntime) InvalidateTriggersByRoom(roomID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for id, t := range r.triggers {
		if t.RoomID == roomID {
			t.Status = StatusInvalid
			t.RoomValid = false
			t.InvalidReason = "room_deleted"
			r.unregisterTrigger(id)
			log.Printf("Invalidated trigger %s: room %s deleted", id, roomID)
		}
	}
}

// ListTriggers 列出所有触发器
func (r *TriggerRuntime) ListTriggers() []*Trigger {
	r.mu.RLock()
	defer r.mu.RUnlock()

	triggers := make([]*Trigger, 0, len(r.triggers))
	for _, t := range r.triggers {
		triggers = append(triggers, t)
	}
	return triggers
}
