# 触发器模块技术设计文档

> 本文档描述 x-client 触发器模块的设计方案

## 目录

1. [背景与目标](#1-背景与目标)
2. [整体架构](#2-整体架构)
3. [触发器类型](#3-触发器类型)
4. [数据库设计](#4-数据库设计)
5. [API 设计](#5-api-设计)
6. [组件设计](#6-组件设计)
7. [聊天室生命周期与触发器关系](#7-聊天室生命周期与触发器关系)
8. [消息流转](#8-消息流转)

---

## 1. 背景与目标

### 1.1 问题

x-client 作为 Agent 的插件，当前只能被动响应用户消息和 @ 提及。Agent 无法主动执行定时任务或根据事件驱动执行后续操作。

### 1.2 目标

1. **定时执行**：支持 Cron 表达式和固定间隔，让 Agent 能定时执行任务
2. **事件驱动**：支持 Webhook 和消息事件触发
3. **轮询检测**：支持 HTTP 轮询，带变更检测
4. **自主管理**：触发器由 X-Client 自主管理，无需 Agent 主动调用
5. **房间绑定**：触发器必须绑定到具体的聊天室
6. **级联失效**：聊天室删除时，关联触发器自动失效

---

## 2. 整体架构

### 2.1 设计原则

1. **触发器属于 X-Client**：每个 X-Client 实例拥有自己的触发器
2. **房间必须存在**：创建触发器时必须指定一个已存在的聊天室
3. **失效而非删除**：聊天室删除时，触发器标记为失效状态，而非物理删除
4. **自主运行**：触发器由 X-Client Runtime 自主调度，不依赖 Agent 调用

### 2.2 架构图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                      X-Client HTTP (:8001)                              │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │  触发器运行时 (TriggerRuntime)                                      │ │
│  │  • 从配置文件加载触发器                                             │ │
│  │  • 从数据库加载触发器                                               │ │
│  │  • 定时检查 (每 15 秒)                                            │ │
│  │  • 触发时 → 发送消息到 Coordinator                                 │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │  触发器管理 API                                                    │ │
│  │  • POST /api/trigger/register  - 注册触发器                       │ │
│  │  • PATCH /api/trigger/{id}     - 更新触发器                       │ │
│  │  • DELETE /api/trigger/{id}    - 删除触发器                       │ │
│  │  • GET /api/trigger/list       - 列出触发器                       │ │
│  └───────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ HTTP 调用
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                      Coordinator HTTP (:8080)                            │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │  聊天室管理 (已有)                                                 │ │
│  │  • 创建/获取/删除聊天室                                            │ │
│  │  • 聊天室删除时 → 通知 X-Client 使关联触发器失效                  │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │  触发器通知 API                                                    │ │
│  │  • POST /api/trigger/notify    - 触发器触发通知                   │ │
│  │  • POST /api/room/deleted      - 聊天室删除通知                    │ │
│  └───────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ 广播消息
                                    ▼
                              聊天室 Room
                                    │
                                    │ @Agent 消息
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                      AgentCore (Claude Agent SDK)                        │
│  • 接收 Intent=TRIGGER 消息                                            │
│  • 由 LLM 决定如何响应                                                  │
└─────────────────────────────────────────────────────────────────────────┘
```

### 2.3 组件职责

| 组件 | 职责 |
|------|------|
| **TriggerRuntime** | 触发器运行时，自身调度触发器，不依赖 Agent 调用 |
| **TriggerManager** | 触发器 CRUD 管理 |
| **RoomWatcher** | 监听聊天室删除事件，失效关联触发器 |
| **CoordinatorClient** | 与 Coordinator 通信，发送触发通知 |

---

## 3. 触发器类型

### 3.1 类型定义

| 类型 | 说明 | 配置字段 |
|------|------|----------|
| `cron` | Cron 表达式定时触发 | `{ "expr": "0 9 * * 1-5" }` |
| `once` | 单次定时触发 | `{ "at": "2026-07-01T09:00:00+08:00" }` |
| `interval` | 固定间隔触发 | `{ "minutes": 30 }` 或 `{ "seconds": 60 }` |
| `poll` | HTTP 轮询 + 变更检测 | `{ "url": "...", "json_path": "$.status", "compare_value": "running" }` |
| `webhook` | Webhook 触发 | `{ "path": "/webhook/trigger/{id}" }` |
| `on_message` | 消息事件触发 | `{ "room_id": "xxx", "from_agent": "xxx", "keywords": ["完成", "结束"] }` |

### 3.2 Cron 表达式示例

```
0 9 * * 1-5        # 每个工作日 9:00
0 */2 * * *       # 每 2 小时
30 18 * * *       # 每天 18:30
0 9,18 * * *       # 每天 9:00 和 18:00
```

### 3.3 状态定义

| 状态 | 说明 |
|------|------|
| `enabled` | 正常启用 |
| `disabled` | 手动禁用 |
| `invalid` | 失效（关联聊天室已删除） |
| `expired` | 已过期（单次触发或达到最大次数） |

---

## 4. 数据库设计

### 4.1 触发器表 (triggers)

```sql
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
    invalid_reason VARCHAR(200) COMMENT '失效原因，如：room_deleted',
    last_fired_at BIGINT COMMENT '上次触发时间戳',
    fire_count INT DEFAULT 0 COMMENT '触发次数',
    max_fires INT COMMENT '最大触发次数，NULL=无限',
    cooldown_seconds INT DEFAULT 60 COMMENT '冷却时间(秒)',
    expires_at BIGINT COMMENT '过期时间戳',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    UNIQUE KEY uk_xclient_trigger_name (xclient_id, name),
    INDEX idx_xclient_id (xclient_id),
    INDEX idx_room_id (room_id),
    INDEX idx_status (status),
    INDEX idx_type (type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 4.2 触发器执行记录表 (trigger_executions)

```sql
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
```

### 4.3 轮询状态表 (poll_states)

```sql
CREATE TABLE IF NOT EXISTS poll_states (
    trigger_id VARCHAR(64) PRIMARY KEY,
    last_value TEXT COMMENT '上次轮询值',
    last_checked_at BIGINT NOT NULL,
    FOREIGN KEY (trigger_id) REFERENCES triggers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 4.4 关键设计说明

1. **xclient_id 绑定**：触发器必须属于某个 X-Client 实例
2. **room_id 必须存在**：创建时 Coordinator 会验证聊天室是否存在
3. **room_valid 字段**：标记聊天室是否仍然有效
4. **invalid_reason**：记录失效原因，便于排查

---

## 5. API 设计

### 5.1 触发器管理 API (X-Client HTTP)

#### 5.1.1 注册触发器

```
POST /api/trigger/register
```

Request:
```json
{
  "name": "daily_report",
  "type": "cron",
  "config": {
    "expr": "0 9 * * 1-5"
  },
  "reason": "每天早九点发送日报",
  "room_id": "room_001",
  "max_fires": null,
  "cooldown_seconds": 60
}
```

Response:
```json
{
  "success": true,
  "trigger_id": "trig_xxx",
  "next_fire_at": 1719795600
}
```

**校验规则**：
- `room_id` 必须在 Coordinator 中存在
- 同一 X-Client 下，`name` 不能重复

#### 5.1.2 更新触发器

```
PATCH /api/trigger/{trigger_id}
```

Request:
```json
{
  "is_enabled": true,
  "config": {
    "expr": "0 10 * * 1-5"
  },
  "room_id": "room_002"
}
```

**特殊逻辑**：
- 如果更新 `room_id`，新聊天室也必须存在
- 如果从失效状态恢复，需要指定新的 `room_id`

#### 5.1.3 删除触发器

```
DELETE /api/trigger/{trigger_id}
```

#### 5.1.4 列出触发器

```
GET /api/trigger/list?status=enabled
```

Response:
```json
{
  "triggers": [
    {
      "id": "trig_xxx",
      "name": "daily_report",
      "type": "cron",
      "config": {"expr": "0 9 * * 1-5"},
      "status": "enabled",
      "room_id": "room_001",
      "room_valid": true,
      "last_fired_at": 1719795600,
      "next_fire_at": 1719882000,
      "fire_count": 5
    },
    {
      "id": "trig_yyy",
      "name": "old_task",
      "type": "interval",
      "status": "invalid",
      "room_id": "room_deleted",
      "room_valid": false,
      "invalid_reason": "room_deleted",
      "next_fire_at": null
    }
  ]
}
```

### 5.2 Coordinator 聊天室删除通知 API

#### 5.2.1 聊天室删除时通知 X-Client

```
POST /api/xclient/room-deleted
```

Request:
```json
{
  "room_id": "room_001"
}
```

Coordinator 在删除聊天室时，调用所有订阅了该房间的 X-Client 的此接口。

### 5.3 触发器配置加载 (X-Client 启动时)

#### 5.3.1 从配置文件加载

`config.json`:
```json
{
  "triggers": [
    {
      "name": "daily_report",
      "type": "cron",
      "config": { "expr": "0 9 * * 1-5" },
      "reason": "发送日报",
      "room_id": "room_001",
      "cooldown_seconds": 60
    }
  ]
}
```

---

## 6. 组件设计

### 6.1 TriggerRuntime

```go
// TriggerRuntime 触发器运行时
type TriggerRuntime struct {
    mu          sync.RWMutex
    triggers    map[string]*Trigger          // trigger_id -> Trigger
    cronJobs    map[string]cron.EntryID       // trigger_id -> cron job
    cron        *cron.Cron
    intervals   map[string]*time.Ticker       // trigger_id -> interval ticker
    polls       map[string]*PollWatcher        // trigger_id -> poll watcher
    xclientID   string                         // X-Client 实例 ID
    coordinator *CoordinatorClient
    db          *sql.DB
}

// Trigger 触发器定义
type Trigger struct {
    ID              string          `json:"id"`
    XClientID       string          `json:"xclient_id"`
    Name            string          `json:"name"`
    Type            string          `json:"type"`
    Config          json.RawMessage `json:"config"`
    Reason          string          `json:"reason"`
    RoomID          string          `json:"room_id"`
    RoomValid       bool            `json:"room_valid"`
    Status          string          `json:"status"`          // enabled|disabled|invalid|expired
    InvalidReason   string          `json:"invalid_reason,omitempty"`
    LastFiredAt     int64           `json:"last_fired_at"`
    FireCount       int             `json:"fire_count"`
    MaxFires        *int           `json:"max_fires,omitempty"`
    CooldownSeconds int             `json:"cooldown_seconds"`
    ExpiresAt       *int64          `json:"expires_at,omitempty"`
    NextFireAt      int64           `json:"next_fire_at,omitempty"`
}
```

### 6.2 初始化与启动

```go
// NewTriggerRuntime 创建触发器运行时
func NewTriggerRuntime(xclientID string, coordinator *CoordinatorClient, db *sql.DB) *TriggerRuntime {
    return &TriggerRuntime{
        triggers:    make(map[string]*Trigger),
        cronJobs:    make(map[string]cron.EntryID),
        cron:        cron.New(),
        intervals:   make(map[string]*time.Ticker),
        polls:       make(map[string]*PollWatcher),
        xclientID:   xclientID,
        coordinator: coordinator,
        db:          db,
    }
}

// Start 启动触发器运行时
func (r *TriggerRuntime) Start(ctx context.Context) error {
    // 启动 Cron 调度器
    r.cron.Start()
    
    // 1. 从配置文件加载触发器
    r.loadFromConfig()
    
    // 2. 从数据库加载触发器
    triggers, err := r.loadFromDB()
    if err != nil {
        return err
    }
    
    for _, t := range triggers {
        if err := r.registerTrigger(t); err != nil {
            log.Printf("Failed to register trigger %s: %v", t.ID, err)
        }
    }
    
    // 3. 启动定时同步（检查聊天室有效性）
    go r.syncLoop(ctx)
    
    return nil
}
```

### 6.3 触发器注册

```go
// registerTrigger 注册触发器到运行时
func (r *TriggerRuntime) registerTrigger(t *Trigger) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    // 只有 enabled 状态且 room_valid 的触发器才会被注册
    if t.Status != "enabled" || !t.RoomValid {
        log.Printf("Skipping trigger %s: status=%s, room_valid=%v", t.ID, t.Status, t.RoomValid)
        return nil
    }
    
    r.triggers[t.ID] = t
    
    switch t.Type {
    case "cron":
        return r.registerCron(t)
    case "interval":
        return r.registerInterval(t)
    case "poll":
        return r.registerPoll(t)
    case "once":
        return r.registerOnce(t)
    case "webhook":
        // Webhook 由外部调用触发，无需注册到调度器
    case "on_message":
        // 消息事件由消息处理器处理
    }
    
    return nil
}
```

### 6.4 聊天室失效处理

```go
// InvalidateTriggersByRoom 使指定聊天室的所有触发器失效
func (r *TriggerRuntime) InvalidateTriggersByRoom(roomID string, reason string) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    affected := []string{}
    
    for id, t := range r.triggers {
        if t.RoomID == roomID {
            t.RoomValid = false
            t.Status = "invalid"
            t.InvalidReason = reason
            t.NextFireAt = 0
            
            // 从调度器移除
            if entryID, ok := r.cronJobs[id]; ok {
                r.cron.Remove(entryID)
                delete(r.cronJobs, id)
            }
            if ticker, ok := r.intervals[id]; ok {
                ticker.Stop()
                delete(r.intervals, id)
            }
            
            affected = append(affected, id)
        }
    }
    
    // 批量更新数据库
    if err := r.updateTriggersStatusDB(affected, "invalid", reason); err != nil {
        return err
    }
    
    log.Printf("Invalidated %d triggers for room %s: %v", len(affected), roomID, affected)
    return nil
}
```

### 6.5 Coordinator 删除聊天室时通知 X-Client

```go
// Coordinator: 删除聊天室前，通知关联的 X-Client
func (h *Handler) DeleteRoomHandler(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    roomID := vars["room_id"]
    
    // 1. 获取聊天室信息（包含创建者 xclient_id）
    room, err := h.storage.GetRoom(roomID)
    if err != nil || room == nil {
        h.writeError(w, http.StatusNotFound, "聊天室不存在")
        return
    }
    
    // 2. 删除聊天室
    if err := h.storage.DeleteRoom(roomID); err != nil {
        h.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    
    // 3. 通知 X-Client 使关联触发器失效
    // 查找所有订阅了该房间的 X-Client
    xclients, _ := h.storage.GetXClientsByRoom(roomID)
    for _, xc := range xclients {
        // 调用 X-Client 的失效接口
        go func(endpoint string) {
            resp, err := http.Post(endpoint+"/api/trigger/room-deleted", 
                "application/json", 
                strings.NewReader(`{"room_id":"`+roomID+`"}`))
            if err != nil {
                log.Printf("Failed to notify xclient %s: %v", endpoint, err)
            } else {
                resp.Body.Close()
            }
        }(xc.Endpoint)
    }
    
    // 4. 软删除触发器（标记为 invalid）
    h.storage.InvalidateTriggersByRoom(roomID, "room_deleted")
    
    h.writeJSON(w, http.StatusOK, gin.H{"success": true})
}
```

---

## 7. 聊天室生命周期与触发器关系

### 7.1 关系图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              聊天室                                      │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   创建 ──────────────────────────────────────────────────────────────┐   │
│   │                                                               │   │
│   ▼                                                               │   │
│   有效 ─────────────────────┐                                      │   │
│   │                         │                                      │   │
│   │  ┌──────────────────────┼──────────────────────┐              │   │
│   │  ▼                      ▼                      ▼              │   │
│   │  触发器A              触发器B               触发器C             │   │
│   │  (enabled)            (enabled)             (enabled)          │   │
│   │                                                               │   │
│   │  正常运行                                                         │   │
│   │                                                               │   │
│   ▼                                                               │   │
│   删除 ────────────────────────────────────────────────────────────┼──►│
│   │                                                               │   │
│   ▼                                                               │   │
│   无效 ─────────────────────┐                                      │   │
│   │                         │                                      │   │
│   │  ┌──────────────────────┼──────────────────────┐              │   │
│   │  ▼                      ▼                      ▼              │   │
│   │  触发器A              触发器B               触发器C             │   │
│   │  (invalid)            (invalid)             (invalid)          │   │
│   │  room_valid=false     room_valid=false      room_valid=false  │   │
│   │                                                               │   │
│   │  停止运行，管理员需要重新绑定聊天室                                │   │
│   │                                                               │   │
│   └───────────────────────────────────────────────────────────────┘   │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 7.2 触发器状态流转

```
                    ┌─────────────────┐
                    │                 │
          ┌────────►│    enabled      │◄────────┐
          │         │                 │         │
          │         └────────┬────────┘         │
          │                  │                  │
          │                  │ 禁用             │ 重新启用
          │                  ▼                  │
          │         ┌─────────────────┐         │
          │         │                 │         │
          │         │   disabled      │─────────┘
          │         │                 │
          │         └─────────────────┘
          │
          │ 聊天室删除
          │
          │         ┌─────────────────┐
          │         │                 │
          └────────►│    invalid     │─────────┐
                    │                 │         │
                    └─────────────────┘         │
                                               │ 重新绑定新聊天室
                                               │ 并启用
                                               ▼
```

### 7.3 管理员操作

| 操作 | 说明 |
|------|------|
| 查看失效触发器 | `GET /api/trigger/list?status=invalid` |
| 编辑失效触发器 | `PATCH /api/trigger/{id}` - 必须指定新的 `room_id` |
| 启用触发器 | `PATCH /api/trigger/{id}` - 设置 `is_enabled: true` |

---

## 8. 消息流转

### 8.1 Cron/Interval 触发流程

```
1. TriggerRuntime 每 15 秒检查所有触发器
2. 计算下次触发时间，判断是否到达
3. 检查 room_valid 状态
4. 触发时，POST /api/trigger/notify 到 Coordinator
5. Coordinator 存储 Intent=TRIGGER 消息，广播到聊天室
6. X-Client 轮询收到消息
7. X-Client 转发给 AgentCore
8. AgentCore 处理消息，LLM 决定响应
```

### 8.2 聊天室删除流程

```
1. 管理员调用 DELETE /api/rooms/{room_id}
2. Coordinator 查询订阅该房间的 X-Client 列表
3. Coordinator 删除聊天室
4. Coordinator 调用每个 X-Client 的 /api/trigger/room-deleted
5. X-Client 更新触发器状态为 invalid
6. X-Client 从调度器移除这些触发器
7. Coordinator 软删除触发器记录
```

### 8.3 触发消息格式

```json
{
  "msg_id": "msg_xxx",
  "room_id": "room_001",
  "sender_id": "system",
  "sender_type": "system",
  "content": "[触发器] daily_report 已触发: 每天早九点发送日报",
  "intent": "TRIGGER",
  "target_id": "all",
  "metadata": {
    "trigger_id": "trig_xxx",
    "xclient_id": "xc_001",
    "trigger_type": "cron",
    "trigger_name": "daily_report",
    "reason": "每天早九点发送日报",
    "fire_count": 5
  }
}
```

---

## 9. 与 Clawith 的差异

| 特性 | Clawith | x-client 触发器 |
|------|---------|----------------|
| 归属 | 属于 Agent | 属于 X-Client |
| 房间绑定 | 无 | 必须绑定聊天室 |
| 级联失效 | 无 | 聊天室删除时触发器失效 |
| 后台调度 | 独立 Daemon | 集成在 X-Client |
| 配置方式 | 数据库/API | 配置文件 + 数据库 |
| 失效恢复 | 无 | 管理员重新绑定 |

---

## 10. 依赖

| 依赖 | 用途 |
|------|------|
| `github.com/robfig/cron/v3` | Cron 调度 |
| `github.com/jmespath/go-jmespath` | JSONPath 解析 |
| `github.com/go-sql-driver/mysql` | MySQL 驱动 |
