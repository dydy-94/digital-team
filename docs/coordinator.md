# Coordinator 实现文档

## 1. 组件概述

**Coordinator** 是整个多智能体群聊系统的消息中枢，负责：
- 管理所有聊天室的元数据
- 路由和广播消息
- 控制发言冲突（Speaker Lock）
- 持久化聊天记录
- 维护 WebSocket 连接

**监听端口**：`:8080` (默认)

---

## 2. 基本能力

### 2.1 聊天室管理
- 创建聊天室
- 删除聊天室
- 查询聊天室列表
- 自动加载数据库中的聊天室

### 2.2 消息路由
- 消息广播到聊天室所有成员
- 支持 @ 提及特定 Agent
- 发言冲突控制

### 2.3 WebSocket 连接管理
- 支持 WebSocket 升级
- 客户端注册/注销
- 心跳检测
- 自动重连支持

### 2.4 数据持久化
- 支持 SQLite (默认) 和 MySQL
- 持久化聊天室、成员、消息

---

## 3. 实现方案

### 3.1 核心组件

```
┌─────────────────────────────────────────────────────────────┐
│                        Hub                                   │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐        │
│  │ Register│  │Unregister│ │JoinChannel│ │  Speak  │        │
│  │  通道   │  │  通道    │  │   通道    │  │  通道   │        │
│  └─────────┘  └─────────┘  └─────────┘  └─────────┘        │
│                                                              │
│  ┌─────────────────────────────────────────────────────┐     │
│  │              ChannelRoom (聊天室)                     │     │
│  │  - members: map[Client]string                       │     │
│  │  - messageHistory: []*A2AMessage                    │     │
│  │  - speakerLock: sync.Mutex                         │     │
│  └─────────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────┐
│                        Client                                │
│  - hub: *Hub                                                │
│  - conn: *websocket.Conn                                    │
│  - send: chan *ServerMessage                               │
│  - agentId: string                                         │
│  - channelId: string                                       │
│                                                              │
│  ┌────────────┐  ┌────────────┐                            │
│  │ readPump() │  │ writePump()│                            │
│  └────────────┘  └────────────┘                            │
└─────────────────────────────────────────────────────────────┘
```

### 3.2 Hub (消息中心)

Hub 是 Coordinator 的核心组件，采用 Go Channel 模式处理并发：

```go
type Hub struct {
    rooms       map[string]*ChannelRoom  // 所有聊天室
    clients     map[*Client]bool         // 所有连接
    Register    chan *Client             // 注册通道
    Unregister  chan *Client             // 注销通道
    JoinChannel chan *JoinEvent           // 加入频道事件
    Speak       chan *SpeakEvent          // 发言事件
    mu          sync.RWMutex              // 读写锁
    storage     storage.Storage           // 存储接口
}
```

#### Hub.Run() 事件处理循环

```go
func (h *Hub) Run() {
    for {
        select {
        case client := <-h.Register:
            // 处理新连接注册
            
        case client := <-h.Unregister:
            // 处理连接断开
            
        case event := <-h.JoinChannel:
            // 处理加入聊天室
            
        case event := <-h.Speak:
            // 处理消息发送
        }
    }
}
```

### 3.3 ChannelRoom (聊天室)

每个聊天室独立维护：

```go
type ChannelRoom struct {
    ID             string                 // 聊天室 ID
    name           string                 // 聊天室名称
    createdAt      int64                  // 创建时间
    members        map[*Client]string     // 成员列表 (Client -> AgentID)
    messageHistory  []*A2AMessage          // 消息历史
    maxHistory     int                    // 最大历史消息数
    
    speakerLock    sync.Mutex             // 发言锁
    currentSpeaker string                 // 当前发言者
    lastSpeakTime  time.Time              // 最后发言时间
    
    mu             sync.RWMutex           // 读写锁
    storage        storage.Storage         // 存储接口
}
```

### 3.4 Speaker Lock (发言控制)

```go
// 尝试获取发言锁
func (r *ChannelRoom) TryAcquireSpeakerLock(agentId string, timeoutMs int64) bool {
    deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
    for {
        r.speakerLock.Lock()
        if r.currentSpeaker == "" || r.currentSpeaker == agentId {
            r.currentSpeaker = agentId
            r.lastSpeakTime = time.Now()
            r.speakerLock.Unlock()
            return true
        }
        r.speakerLock.Unlock()
        
        if time.Now().After(deadline) {
            return false
        }
        time.Sleep(100 * time.Millisecond)
    }
}
```

### 3.5 消息广播

```go
func (r *ChannelRoom) Broadcast(message *ServerMessage, excludeClient *Client) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    for client := range r.members {
        if client != excludeClient {
            client.Send(message)
        }
    }
}
```

### 3.6 自动加入机制

当 Agent 重新连接时，自动加入之前所在的聊天室：

```go
case "register":
    // ... 设置 agentId
    member, err := c.hub.storage.GetMemberByAgentID(data.AgentId)
    if err == nil && member != nil && member.RoomID != "" {
        c.hub.JoinChannel <- &JoinEvent{
            Client:    c,
            ChannelId: member.RoomID,
            AgentId:   data.AgentId,
        }
    }
```

---

## 4. API 文档

### 4.1 WebSocket 端点

#### `/ws/chat` - WebSocket 连接

**协议升级请求示例**：
```
GET /ws/chat HTTP/1.1
Host: localhost:8080
Upgrade: websocket
Connection: Upgrade
```

### 4.2 HTTP API

#### 健康检查

**GET** `/health`

**响应**：
```
OK
```

---

#### 获取聊天室列表

**GET** `/api/rooms`

**响应**：
```json
{
  "success": true,
  "rooms": [
    {
      "id": "room_test_123",
      "name": "test",
      "agents": ["agent_1", "agent_2"],
      "created": 1234567890
    }
  ]
}
```

---

#### 创建聊天室

**POST** `/api/room/create`

**请求体**：
```json
{
  "name": "test_room",
  "agents": ["agent_1", "agent_2"]
}
```

**响应**：
```json
{
  "success": true,
  "room_id": "room_test_room_1234567890"
}
```

---

#### 删除聊天室

**DELETE** `/api/room/delete`

**请求体**：
```json
{
  "room_id": "room_test_room_1234567890"
}
```

**响应**：
```json
{
  "success": true,
  "message": "Room deleted successfully"
}
```

---

#### 获取指标

**GET** `/metrics`

**响应**：
```json
{
  "websocket_connections": 5,
  "speak_conflicts": 2
}
```

---

## 5. WebSocket 消息协议

### 5.1 客户端发送消息

#### 注册
```json
{
  "action": "register",
  "data": {
    "agentId": "agent_1"
  }
}
```

#### 加入聊天室
```json
{
  "action": "join",
  "data": {
    "channelId": "room_test_123",
    "agentId": "agent_1"
  }
}
```

#### 离开聊天室
```json
{
  "action": "leave"
}
```

#### 发送消息
```json
{
  "action": "speak",
  "data": {
    "msgId": "uuid-string",
    "channelId": "room_test_123",
    "sender": "agent_1",
    "target": "ALL",
    "mentionUsers": ["agent_2"],
    "intent": "INFORM",
    "contentText": "大家好，我是 agent_1"
  }
}
```

#### 同步历史消息
```json
{
  "action": "sync",
  "data": {
    "channelId": "room_test_123",
    "count": 10
  }
}
```

#### 心跳
```json
{
  "action": "heartbeat"
}
```

### 5.2 服务端发送消息

#### 新消息
```json
{
  "type": "message",
  "data": {
    "msgId": "uuid-string",
    "channelId": "room_test_123",
    "sender": "agent_1",
    "target": "ALL",
    "mentionUsers": ["agent_2"],
    "intent": "INFORM",
    "contentText": "大家好",
    "timestamp": 1234567890
  }
}
```

#### 历史消息
```json
{
  "type": "history",
  "data": [
    {
      "msgId": "uuid-1",
      "channelId": "room_test_123",
      "sender": "agent_1",
      "contentText": "消息1",
      "timestamp": 1234567890
    }
  ]
}
```

#### 错误消息
```json
{
  "type": "error",
  "data": "错误描述"
}
```

#### 发言被拒绝
```json
{
  "type": "reject",
  "data": {
    "msgId": "original-msg-id",
    "reason": "Agent agent_2 正在发言，请稍后"
  }
}
```

---

## 6. 配置说明

### 6.1 配置文件格式

创建 `config.json` 文件：

```json
{
  "listen_addr": ":8080",
  "max_history": 50,
  "speaker_lock_timeout_ms": 2000,
  "speaker_cooldown_ms": 2000,
  "heartbeat_interval_seconds": 30,
  "reconnect_interval_seconds": 5,
  "storage_type": "sqlite",
  "storage_path": "data/coordinator.db",
  "mysql_host": "localhost",
  "mysql_port": 3306,
  "mysql_user": "root",
  "mysql_password": "password",
  "mysql_database": "coordinator"
}
```

### 6.2 配置项说明

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| listen_addr | :8080 | HTTP/WebSocket 监听地址 |
| max_history | 50 | 每个聊天室保留的最大历史消息数 |
| speaker_lock_timeout_ms | 2000 | 获取发言锁的超时时间(毫秒) |
| speaker_cooldown_ms | 2000 | 发言冷却时间(毫秒) |
| heartbeat_interval_seconds | 30 | 心跳间隔(秒) |
| reconnect_interval_seconds | 5 | 重连间隔(秒) |
| storage_type | sqlite | 存储类型 (sqlite/mysql) |

### 6.3 命令行参数

```bash
./bin/coordinator -config config.json
```

---

## 7. 目录结构

```
coordinator/
├── bin/
│   └── coordinator          # 编译后的可执行文件
├── data/
│   └── coordinator.db       # SQLite 数据库文件
├── main.go                 # 程序入口
├── config.go               # 配置加载
├── hub.go                  # Hub 核心实现
├── client.go               # Client 连接处理
├── room.go                 # ChannelRoom 实现
├── message.go              # 消息类型定义
├── logging.go              # 日志工具
├── metrics.go              # 指标收集
└── go.mod
```

---

## 8. 错误处理

### 8.1 常见错误

| 错误 | 说明 | 处理方式 |
|------|------|----------|
| WebSocket 升级失败 | 连接非 WebSocket 请求 | 返回错误，关闭连接 |
| 发言锁获取超时 | 其他 Agent 正在发言 | 返回 reject 消息 |
| 聊天室不存在 | 尝试加入不存在的聊天室 | 自动创建聊天室 |
| 存储操作失败 | 数据库错误 | 记录日志，继续运行 |

### 8.2 Panic 恢复

```go
func (c *Client) Send(msg *ServerMessage) {
    defer func() {
        if r := recover(); r != nil {
            logWarn("client", "Send panicked", "agent_id", c.agentId)
        }
    }()
    // ...
}
```

---

## 9. 性能考虑

### 9.1 并发模型
- 使用 Go Channel 进行事件驱动
- 读写锁保护共享状态
- 无锁消息广播

### 9.2 内存优化
- 历史消息限制在 50 条
- 定期清理消息 ID 缓存
- 使用 sync.Pool 复用对象

### 9.3 连接管理
- 心跳检测断开连接
- 优雅关闭
- 限制每个客户端的发送缓冲区
