# 触发器模块实施步骤

> 本文档描述 x-client 触发器模块的详细实施步骤

## 目录

1. [实施阶段总览](#1-实施阶段总览)
2. [阶段一：Coordinator 聊天室管理增强](#2-阶段一coordinator-聊天室管理增强)
3. [阶段二：数据库设计](#3-阶段二数据库设计)
4. [阶段三：Coordinator 触发器 API](#4-阶段三coordinator-触发器-api)
5. [阶段四：X-Client 触发器运行时](#5-阶段四x-client-触发器运行时)
6. [阶段五：集成与测试](#6-阶段五集成与测试)

---

## 1. 实施阶段总览

| 阶段 | 内容 | 优先级 | 预计工时 |
|------|------|--------|----------|
| Phase 1 | Coordinator 聊天室管理增强 | P0 | 0.5 天 |
| Phase 2 | 数据库设计 | P0 | 0.5 天 |
| Phase 3 | Coordinator 触发器 API | P0 | 1 天 |
| Phase 4 | X-Client 触发器运行时 | P0 | 2 天 |
| Phase 5 | 集成与测试 | P1 | 1 天 |

---

## 2. 阶段一：Coordinator 聊天室管理增强

### 2.1 需要增强的功能

1. **聊天室删除时通知 X-Client**
2. **记录聊天室订阅的 X-Client 列表**

### 2.2 改动文件

| 文件 | 改动 |
|------|------|
| `coordinator-http/storage.go` | 新增方法 |
| `coordinator-http/models.go` | 新增 RoomSubscription 模型 |
| `coordinator-http/handler.go` | 增强 DeleteRoomHandler |

### 2.3 模型新增

```go
// RoomSubscription 聊天室订阅关系
type RoomSubscription struct {
    ID         int64     `json:"-" db:"id"`
    RoomID     string    `json:"room_id" db:"room_id"`
    XClientID  string    `json:"xclient_id" db:"xclient_id"`
    XClientEndpoint string `json:"xclient_endpoint" db:"xclient_endpoint"`
    CreatedAt  time.Time `json:"created_at" db:"created_at"`
}
```

### 2.4 Storage 新增方法

```go
// storage.go

// SubscribeRoom 订阅聊天室
func (s *MySQLStorage) SubscribeRoom(roomID, xclientID, endpoint string) error {
    _, err := s.db.Exec(`
        INSERT INTO room_subscriptions (room_id, xclient_id, xclient_endpoint, created_at)
        VALUES (?, ?, ?, NOW())
        ON DUPLICATE KEY UPDATE xclient_endpoint = VALUES(xclient_endpoint)
    `, roomID, xclientID, endpoint)
    return err
}

// UnsubscribeRoom 取消订阅聊天室
func (s *MySQLStorage) UnsubscribeRoom(roomID, xclientID string) error {
    _, err := s.db.Exec(`
        DELETE FROM room_subscriptions WHERE room_id = ? AND xclient_id = ?
    `, roomID, xclientID)
    return err
}

// GetRoomSubscribers 获取聊天室的所有订阅者
func (s *MySQLStorage) GetRoomSubscribers(roomID string) ([]RoomSubscription, error) {
    rows, err := s.db.Query(`
        SELECT room_id, xclient_id, xclient_endpoint FROM room_subscriptions WHERE room_id = ?
    `, roomID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    
    var subs []RoomSubscription
    for rows.Next() {
        var sub RoomSubscription
        if err := rows.Scan(&sub.RoomID, &sub.XClientID, &sub.XClientEndpoint); err != nil {
            return nil, err
        }
        subs = append(subs, sub)
    }
    return subs, nil
}

// InvalidateTriggersByRoom 使聊天室关联的触发器失效
func (s *MySQLStorage) InvalidateTriggersByRoom(roomID, reason string) error {
    _, err := s.db.Exec(`
        UPDATE triggers 
        SET status = 'invalid', room_valid = FALSE, invalid_reason = ?, updated_at = UNIX_MILLIS()
        WHERE room_id = ?
    `, reason, roomID)
    return err
}
```

### 2.5 Handler 增强

```go
// handler.go

// DeleteRoomHandler 删除聊天室
func (h *Handler) DeleteRoomHandler(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    roomID := vars["room_id"]
    
    // 1. 检查聊天室是否存在
    room, err := h.storage.GetRoom(roomID)
    if err != nil || room == nil {
        h.writeError(w, http.StatusNotFound, "聊天室不存在")
        return
    }
    
    // 2. 获取聊天室订阅者
    subs, _ := h.storage.GetRoomSubscribers(roomID)
    
    // 3. 删除聊天室
    if err := h.storage.DeleteRoom(roomID); err != nil {
        h.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    
    // 4. 删除聊天室订阅关系
    h.storage.DeleteRoomSubscriptions(roomID)
    
    // 5. 使关联触发器失效
    h.storage.InvalidateTriggersByRoom(roomID, "room_deleted")
    
    // 6. 异步通知 X-Client
    for _, sub := range subs {
        go func(endpoint string) {
            http.Post(endpoint+"/api/trigger/room-deleted",
                "application/json",
                strings.NewReader(`{"room_id":"`+roomID+`"}`))
        }(sub.XClientEndpoint)
    }
    
    h.writeJSON(w, http.StatusOK, gin.H{"success": true})
}
```

### 2.6 新增 SQL

```sql
-- room_subscriptions 表
CREATE TABLE IF NOT EXISTS room_subscriptions (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    room_id VARCHAR(64) NOT NULL,
    xclient_id VARCHAR(64) NOT NULL,
    xclient_endpoint VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_room_xclient (room_id, xclient_id),
    INDEX idx_room_id (room_id),
    INDEX idx_xclient_id (xclient_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 3. 阶段二：数据库设计

### 3.1 触发器 SQL

创建 `sql/triggers.sql`:

```sql
-- 触发器表
CREATE TABLE IF NOT EXISTS triggers (
    id VARCHAR(64) PRIMARY KEY,
    xclient_id VARCHAR(64) NOT NULL COMMENT '所属 X-Client 实例 ID',
    name VARCHAR(100) NOT NULL,
    type VARCHAR(20) NOT NULL COMMENT 'cron|once|interval|poll|webhook|on_message',
    config JSON NOT NULL COMMENT '触发器配置',
    reason TEXT COMMENT '触发原因描述',
    room_id VARCHAR(64) NOT NULL COMMENT '关联的聊天室 ID',
    room_valid BOOLEAN DEFAULT TRUE COMMENT '聊天室是否有效',
    status VARCHAR(20) DEFAULT 'enabled' COMMENT 'enabled|disabled|invalid|expired',
    invalid_reason VARCHAR(200) COMMENT '失效原因',
    last_fired_at BIGINT COMMENT '上次触发时间戳',
    fire_count INT DEFAULT 0 COMMENT '触发次数',
    max_fires INT COMMENT '最大触发次数',
    cooldown_seconds INT DEFAULT 60 COMMENT '冷却时间(秒)',
    expires_at BIGINT COMMENT '过期时间戳',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    UNIQUE KEY uk_xclient_trigger_name (xclient_id, name),
    INDEX idx_xclient_id (xclient_id),
    INDEX idx_room_id (room_id),
    INDEX idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 触发器执行记录表
CREATE TABLE IF NOT EXISTS trigger_executions (
    id VARCHAR(64) PRIMARY KEY,
    trigger_id VARCHAR(64) NOT NULL,
    fired_at BIGINT NOT NULL COMMENT '触发时间戳',
    status VARCHAR(20) DEFAULT 'pending' COMMENT 'pending|success|failed|skipped',
    error_message TEXT,
    execution_time_ms INT COMMENT '执行耗时(毫秒)',
    created_at BIGINT NOT NULL,
    INDEX idx_trigger_id (trigger_id),
    INDEX idx_fired_at (fired_at),
    FOREIGN KEY (trigger_id) REFERENCES triggers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 轮询状态表
CREATE TABLE IF NOT EXISTS poll_states (
    trigger_id VARCHAR(64) PRIMARY KEY,
    last_value TEXT COMMENT '上次轮询值',
    last_checked_at BIGINT NOT NULL,
    FOREIGN KEY (trigger_id) REFERENCES triggers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 4. 阶段三：Coordinator 触发器 API

### 4.1 模型定义

```go
// models.go

// Trigger 触发器
type Trigger struct {
    ID              string          `json:"id"`
    XClientID       string          `json:"xclient_id"`
    Name            string          `json:"name"`
    Type            string          `json:"type"`
    Config          json.RawMessage `json:"config"`
    Reason          string          `json:"reason"`
    RoomID          string          `json:"room_id"`
    RoomValid       bool            `json:"room_valid"`
    Status          string          `json:"status"`
    InvalidReason   string          `json:"invalid_reason,omitempty"`
    LastFiredAt     int64           `json:"last_fired_at,omitempty"`
    FireCount       int             `json:"fire_count"`
    MaxFires        *int           `json:"max_fires,omitempty"`
    CooldownSeconds int             `json:"cooldown_seconds"`
    ExpiresAt       *int64          `json:"expires_at,omitempty"`
    CreatedAt       int64           `json:"created_at"`
    UpdatedAt       int64           `json:"updated_at"`
}

// TriggerNotifyRequest 触发器触发通知
type TriggerNotifyRequest struct {
    TriggerID   string `json:"trigger_id" binding:"required"`
    XClientID   string `json:"xclient_id" binding:"required"`
    TriggerType string `json:"trigger_type" binding:"required"`
    Reason      string `json:"reason"`
    Payload     map[string]interface{} `json:"payload,omitempty"`
}
```

### 4.2 Handler 实现

```go
// handler.go

// TriggerNotifyHandler 处理触发器触发通知
func (h *Handler) TriggerNotifyHandler(w http.ResponseWriter, r *http.Request) {
    var req TriggerNotifyRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        h.writeError(w, http.StatusBadRequest, err.Error())
        return
    }
    
    // 1. 检查触发器是否存在
    trigger, err := h.storage.GetTrigger(req.TriggerID)
    if err != nil || trigger == nil {
        h.writeError(w, http.StatusNotFound, "触发器不存在")
        return
    }
    
    // 2. 检查触发器状态
    if trigger.Status != "enabled" || !trigger.RoomValid {
        h.writeError(w, http.StatusBadRequest, "触发器未启用或已失效")
        return
    }
    
    // 3. 检查冷却时间
    if trigger.CooldownSeconds > 0 {
        elapsed := time.Now().UnixMilli() - trigger.LastFiredAt
        if elapsed < int64(trigger.CooldownSeconds*1000) {
            h.writeError(w, http.StatusBadRequest, "触发器在冷却中")
            return
        }
    }
    
    // 4. 构造触发消息
    msg := &Message{
        MsgID:      generateMsgID(),
        RoomID:     trigger.RoomID,
        SenderID:   "system",
        SenderType: "system",
        Content:    fmt.Sprintf("[触发器] %s 已触发: %s", trigger.Name, trigger.Reason),
        Intent:     "TRIGGER",
        TargetID:   "all",
        Metadata: map[string]interface{}{
            "trigger_id":    req.TriggerID,
            "xclient_id":    req.XClientID,
            "trigger_type":  req.TriggerType,
            "trigger_name":  trigger.Name,
            "reason":        req.Reason,
            "fire_count":    trigger.FireCount + 1,
        },
        CreatedAt: time.Now(),
    }
    
    // 5. 存储并广播
    if err := h.storage.CreateMessage(msg); err != nil {
        h.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    
    h.broadcastToRoom(trigger.RoomID, msg)
    
    // 6. 更新触发器状态
    h.storage.UpdateTriggerFired(req.TriggerID, time.Now().UnixMilli())
    
    h.writeJSON(w, http.StatusOK, gin.H{"success": true, "msg_id": msg.MsgID})
}

// GetTriggersHandler 获取触发器列表
func (h *Handler) GetTriggersHandler(w http.ResponseWriter, r *http.Request) {
    roomID := r.URL.Query().Get("room_id")
    status := r.URL.Query().Get("status")
    
    triggers, err := h.storage.GetTriggers(roomID, status)
    if err != nil {
        h.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    
    h.writeJSON(w, http.StatusOK, gin.H{"triggers": triggers})
}

// InvalidateTriggerHandler 使触发器失效
func (h *Handler) InvalidateTriggerHandler(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    triggerID := vars["id"]
    
    var req struct {
        Reason string `json:"reason"`
    }
    json.NewDecoder(r.Body).Decode(&req)
    
    if err := h.storage.InvalidateTrigger(triggerID, req.Reason); err != nil {
        h.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    
    h.writeJSON(w, http.StatusOK, gin.H{"success": true})
}
```

### 4.3 Storage 方法

```go
// storage.go

// GetTrigger 获取单个触发器
func (s *MySQLStorage) GetTrigger(id string) (*Trigger, error) {
    row := s.db.QueryRow(`SELECT * FROM triggers WHERE id = ?`, id)
    return s.scanTrigger(row)
}

// GetTriggers 获取触发器列表
func (s *MySQLStorage) GetTriggers(roomID, status string) ([]*Trigger, error) {
    query := `SELECT * FROM triggers WHERE 1=1`
    args := []interface{}{}
    
    if roomID != "" {
        query += ` AND room_id = ?`
        args = append(args, roomID)
    }
    if status != "" {
        query += ` AND status = ?`
        args = append(args, status)
    }
    
    query += ` ORDER BY created_at DESC`
    
    rows, err := s.db.Query(query, args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    
    var triggers []*Trigger
    for rows.Next() {
        t, err := s.scanTriggerRows(rows)
        if err != nil {
            return nil, err
        }
        triggers = append(triggers, t)
    }
    return triggers, nil
}

// UpdateTriggerFired 更新触发器触发状态
func (s *MySQLStorage) UpdateTriggerFired(id string, firedAt int64) error {
    _, err := s.db.Exec(`
        UPDATE triggers 
        SET last_fired_at = ?, fire_count = fire_count + 1, updated_at = ?
        WHERE id = ?
    `, firedAt, time.Now().UnixMilli(), id)
    return err
}

// InvalidateTrigger 使触发器失效
func (s *MySQLStorage) InvalidateTrigger(id, reason string) error {
    _, err := s.db.Exec(`
        UPDATE triggers 
        SET status = 'invalid', room_valid = FALSE, invalid_reason = ?, updated_at = ?
        WHERE id = ?
    `, reason, time.Now().UnixMilli(), id)
    return err
}
```

### 4.4 路由注册

```go
// main.go

func main() {
    r := mux.NewRouter()
    
    // 触发器 API
    r.HandleFunc("/api/trigger/notify", h.TriggerNotifyHandler).Methods("POST")
    r.HandleFunc("/api/triggers", h.GetTriggersHandler).Methods("GET")
    r.HandleFunc("/api/triggers/{id}/invalidate", h.InvalidateTriggerHandler).Methods("POST")
    
    // 聊天室删除增强
    r.HandleFunc("/api/rooms/{room_id}", h.DeleteRoomHandler).Methods("DELETE")
}
```

---

## 5. 阶段四：X-Client 触发器运行时

### 5.1 项目结构

```
x-client-http/
├── trigger_runtime.go    # 触发器运行时
├── trigger_runtime_test.go # 测试
└── main.go               # 集成
```

### 5.2 触发器运行时实现

```go
// trigger_runtime.go
package main

import (
    "context"
    "encoding/json"
    "log"
    "sync"
    "time"
    
    "github.com/robfig/cron/v3"
)

const (
    StatusEnabled  = "enabled"
    StatusDisabled = "disabled"
    StatusInvalid  = "invalid"
    StatusExpired  = "expired"
)

type Trigger struct {
    ID              string          `json:"id"`
    XClientID       string          `json:"xclient_id"`
    Name            string          `json:"name"`
    Type            string          `json:"type"`
    Config          json.RawMessage `json:"config"`
    Reason          string          `json:"reason"`
    RoomID          string          `json:"room_id"`
    RoomValid       bool            `json:"room_valid"`
    Status          string          `json:"status"`
    InvalidReason   string          `json:"invalid_reason,omitempty"`
    LastFiredAt     int64           `json:"last_fired_at"`
    FireCount       int             `json:"fire_count"`
    MaxFires        *int           `json:"max_fires,omitempty"`
    CooldownSeconds int             `json:"cooldown_seconds"`
    NextFireAt      int64           `json:"next_fire_at,omitempty"`
}

type TriggerRuntime struct {
    mu          sync.RWMutex
    triggers    map[string]*Trigger
    cronJobs    map[string]cron.EntryID
    cron        *cron.Cron
    intervals   map[string]*time.Ticker
    xclientID   string
    coordinator *CoordinatorClient
    db          *sql.DB
}

func NewTriggerRuntime(xclientID string, coordinator *CoordinatorClient, db *sql.DB) *TriggerRuntime {
    return &TriggerRuntime{
        triggers:    make(map[string]*Trigger),
        cronJobs:    make(map[string]cron.EntryID),
        cron:        cron.New(),
        intervals:   make(map[string]*time.Ticker),
        xclientID:   xclientID,
        coordinator: coordinator,
        db:          db,
    }
}

func (r *TriggerRuntime) Start(ctx context.Context) error {
    r.cron.Start()
    
    // 从数据库加载
    triggers, err := r.loadFromDB()
    if err != nil {
        return err
    }
    
    for _, t := range triggers {
        if err := r.registerTrigger(t); err != nil {
            log.Printf("Failed to register trigger %s: %v", t.ID, err)
        }
    }
    
    // 启动同步循环
    go r.syncLoop(ctx)
    
    return nil
}

func (r *TriggerRuntime) Stop() {
    r.cron.Stop()
    for _, ticker := range r.intervals {
        ticker.Stop()
    }
}

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
    }
    
    return nil
}

func (r *TriggerRuntime) registerCron(t *Trigger) error {
    var cfg struct {
        Expr string `json:"expr"`
    }
    if err := json.Unmarshal(t.Config, &cfg); err != nil {
        return err
    }
    
    entryID, err := r.cron.AddFunc(cfg.Expr, func() {
        r.fireTrigger(t)
    })
    if err != nil {
        return err
    }
    
    r.cronJobs[t.ID] = entryID
    t.NextFireAt = r.cron.Entry(entryID).Next.UnixMilli()
    
    return nil
}

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
    
    go func() {
        for range ticker.C {
            r.fireTrigger(t)
        }
    }()
    
    t.NextFireAt = time.Now().Add(interval).UnixMilli()
    
    return nil
}

func (r *TriggerRuntime) fireTrigger(t *Trigger) {
    // 冷却检查
    if t.CooldownSeconds > 0 {
        elapsed := time.Now().UnixMilli() - t.LastFiredAt
        if elapsed < int64(t.CooldownSeconds*1000) {
            log.Printf("Trigger %s in cooldown", t.ID)
            return
        }
    }
    
    // 最大触发次数检查
    if t.MaxFires != nil && t.FireCount >= *t.MaxFires {
        r.disableTrigger(t.ID, StatusExpired)
        return
    }
    
    // 更新状态
    t.FireCount++
    t.LastFiredAt = time.Now().UnixMilli()
    
    // 通知 Coordinator
    if err := r.coordinator.NotifyTrigger(t); err != nil {
        log.Printf("Failed to notify trigger: %v", err)
    }
    
    // 更新数据库
    r.updateTriggerDB(t)
}

func (r *TriggerRuntime) disableTrigger(id string, status string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    if t, ok := r.triggers[id]; ok {
        t.Status = status
        if status == StatusInvalid {
            t.RoomValid = false
        }
        
        if entryID, ok := r.cronJobs[id]; ok {
            r.cron.Remove(entryID)
            delete(r.cronJobs, id)
        }
        if ticker, ok := r.intervals[id]; ok {
            ticker.Stop()
            delete(r.intervals, id)
        }
    }
    
    r.updateTriggerStatusDB(id, status)
}

func (r *TriggerRuntime) InvalidateTriggersByRoom(roomID string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    for id, t := range r.triggers {
        if t.RoomID == roomID {
            t.Status = StatusInvalid
            t.RoomValid = false
            t.InvalidReason = "room_deleted"
            t.NextFireAt = 0
            
            if entryID, ok := r.cronJobs[id]; ok {
                r.cron.Remove(entryID)
                delete(r.cronJobs, id)
            }
            if ticker, ok := r.intervals[id]; ok {
                ticker.Stop()
                delete(r.intervals, id)
            }
            
            r.updateTriggerStatusDB(id, StatusInvalid)
        }
    }
}

func (r *TriggerRuntime) syncLoop(ctx context.Context) {
    ticker := time.NewTicker(15 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            r.checkTriggers()
        }
    }
}

func (r *TriggerRuntime) checkTriggers() {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    for id, t := range r.triggers {
        if t.Status == StatusEnabled && t.RoomValid {
            // 检查是否过期
            if t.MaxFires != nil && t.FireCount >= *t.MaxFires {
                r.disableTrigger(id, StatusExpired)
            }
        }
    }
}
```

### 5.3 HTTP API

```go
// trigger_handler.go

// RoomDeletedHandler 处理聊天室删除通知
func (c *XClient) RoomDeletedHandler(ctx *gin.Context) {
    var req struct {
        RoomID string `json:"room_id" binding:"required"`
    }
    if err := ctx.ShouldBindJSON(&req); err != nil {
        ctx.JSON(400, gin.H{"error": err.Error()})
        return
    }
    
    c.triggerRuntime.InvalidateTriggersByRoom(req.RoomID)
    
    ctx.JSON(200, gin.H{"success": true})
}

// RegisterTriggerHandler 注册触发器
func (c *XClient) RegisterTriggerHandler(ctx *gin.Context) {
    var req CreateTriggerRequest
    if err := ctx.ShouldBindJSON(&req); err != nil {
        ctx.JSON(400, gin.H{"error": err.Error()})
        return
    }
    
    // 验证聊天室是否存在
    room, err := c.coordinatorClient.GetRoom(req.RoomID)
    if err != nil || room == nil {
        ctx.JSON(400, gin.H{"error": "聊天室不存在"})
        return
    }
    
    trigger := &Trigger{
        ID:              generateID("trig_"),
        XClientID:       c.agentID,
        Name:            req.Name,
        Type:            req.Type,
        Config:          req.Config,
        Reason:          req.Reason,
        RoomID:          req.RoomID,
        RoomValid:       true,
        Status:          StatusEnabled,
        FireCount:       0,
        CooldownSeconds: req.CooldownSeconds,
        MaxFires:        req.MaxFires,
    }
    
    // 保存到数据库
    if err := c.saveTriggerToDB(trigger); err != nil {
        ctx.JSON(500, gin.H{"error": err.Error()})
        return
    }
    
    // 注册到运行时
    if err := c.triggerRuntime.RegisterTrigger(trigger); err != nil {
        ctx.JSON(500, gin.H{"error": err.Error()})
        return
    }
    
    ctx.JSON(200, gin.H{
        "success":      true,
        "trigger_id":   trigger.ID,
        "next_fire_at": trigger.NextFireAt,
    })
}
```

### 5.4 main.go 集成

```go
// main.go

type XClient struct {
    // ... 已有字段
    triggerRuntime *TriggerRuntime
}

func main() {
    // ... 已有初始化
    
    // 初始化触发器运行时
    triggerRuntime := NewTriggerRuntime(cfg.AgentID, coordinator, db)
    if err := triggerRuntime.Start(context.Background()); err != nil {
        log.Printf("Failed to start trigger runtime: %v", err)
    }
    xclient.triggerRuntime = triggerRuntime
    
    // 注册触发器路由
    r.POST("/api/trigger/register", xclient.RegisterTriggerHandler)
    r.POST("/api/trigger/room-deleted", xclient.RoomDeletedHandler)
    r.GET("/api/trigger/list", xclient.ListTriggersHandler)
    r.PATCH("/api/trigger/:id", xclient.UpdateTriggerHandler)
    r.DELETE("/api/trigger/:id", xclient.DeleteTriggerHandler)
    
    // 启动服务器
    log.Printf("X-Client starting on %s", cfg.ListenAddr)
    if err := r.Run(cfg.ListenAddr); err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }
}
```

---

## 6. 阶段五：集成与测试

### 6.1 测试用例

| 测试项 | 说明 |
|--------|------|
| Cron 触发 | 每分钟触发一次，验证消息发送 |
| Interval 触发 | 每 30 秒触发一次 |
| 聊天室删除 | 删除聊天室后验证触发器失效 |
| 触发器恢复 | 失效后绑定新聊天室并启用 |
| 冷却时间 | 验证冷却时间内不重复触发 |
| 最大触发次数 | 达到次数后自动停止 |

### 6.2 测试脚本

```bash
# 1. 注册触发器
curl -X POST http://localhost:8001/api/trigger/register \
  -H "Content-Type: application/json" \
  -d '{
    "name": "test_interval",
    "type": "interval",
    "config": {"minutes": 1},
    "reason": "测试间隔触发器",
    "room_id": "room_001"
  }'

# 2. 查看触发器列表
curl http://localhost:8001/api/trigger/list

# 3. 删除聊天室（验证触发器失效）
curl -X DELETE http://localhost:8080/api/rooms/room_001

# 4. 再次查看触发器（应看到 invalid 状态）
curl http://localhost:8001/api/trigger/list?status=invalid
```

---

## 7. 实施检查清单

### Phase 1 - Coordinator 聊天室管理增强
- [ ] 新增 room_subscriptions 表
- [ ] 实现 SubscribeRoom/UnsubscribeRoom
- [ ] 实现 GetRoomSubscribers
- [ ] 增强 DeleteRoomHandler
- [ ] 单元测试

### Phase 2 - 数据库设计
- [ ] 创建 triggers.sql
- [ ] 创建 trigger_executions.sql
- [ ] 创建 poll_states.sql
- [ ] 运行 SQL 初始化
- [ ] 验证表结构

### Phase 3 - Coordinator 触发器 API
- [ ] 实现 Trigger 模型
- [ ] 实现 TriggerNotifyHandler
- [ ] 实现 GetTriggersHandler
- [ ] 实现 InvalidateTriggerHandler
- [ ] 注册路由
- [ ] 单元测试

### Phase 4 - X-Client 触发器运行时
- [ ] 实现 TriggerRuntime 结构
- [ ] 实现 Start/Stop
- [ ] 实现 Cron 调度
- [ ] 实现 Interval 执行
- [ ] 实现 fireTrigger
- [ ] 实现 InvalidateTriggersByRoom
- [ ] 实现 HTTP Handler
- [ ] 注册路由
- [ ] main.go 集成
- [ ] 单元测试

### Phase 5 - 集成测试
- [ ] Cron 触发端到端测试
- [ ] Interval 触发端到端测试
- [ ] 聊天室删除触发器失效测试
- [ ] 冷却时间测试
- [ ] 最大触发次数测试

---

## 8. 依赖

```go
// go.mod
require (
    github.com/gin-gonic/gin v1.9.1
    github.com/robfig/cron/v3 v3.0.1
    github.com/go-sql-driver/mysql v1.7.1
)
```
