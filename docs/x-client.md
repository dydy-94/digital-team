# x-client 实现文档

## 1. 组件概述

**x-client** 是 Agent 的客户端组件，负责：
- 与 Coordinator 建立 WebSocket 连接
- 处理收到的消息
- 管理对话上下文（Memory Window）
- 调用 AgentCore 获取 AI 响应
- 支持 Skill 回调机制

**监听端口**：`:8001+` (每个 Agent 实例不同)

---

## 2. 基本能力

### 2.1 WebSocket 通信
- 连接 Coordinator
- 发送/接收消息
- 心跳保活
- 自动重连

### 2.2 消息处理
- 区分 @ 消息和旁听消息
- 消息去重
- 历史消息同步

### 2.3 上下文管理
- Memory Window：维护每个聊天室的消息历史
- 自动裁剪超长上下文
- 支持多聊天室切换

### 2.4 AI 集成
- 调用 AgentCore 获取响应
- 支持上下文注入
- 错误处理和重试

### 2.5 HTTP API
- Skill 回调接口
- 健康检查

---

## 3. 实现方案

### 3.1 核心组件

```
┌─────────────────────────────────────────────────────────────┐
│                         XClient                             │
│  agentID: string                                            │
│  coordinatorURL: string                                    │
│  agentCoreURL: string                                      │
│  listenAddr: string                                        │
│                                                              │
│  ┌─────────────────┐  ┌─────────────────┐                 │
│  │   WebSocket     │  │   HTTP Server   │                 │
│  │    Client      │  │    (:port)      │                 │
│  └─────────────────┘  └─────────────────┘                 │
│                                                              │
│  ┌─────────────────┐  ┌─────────────────┐                 │
│  │  Memory Window  │  │  Session        │                 │
│  │  (per channel)  │  │  Manager        │                 │
│  └─────────────────┘  └─────────────────┘                 │
└─────────────────────────────────────────────────────────────┘
```

### 3.2 XClient 结构

```go
type XClient struct {
    agentID           string                    // Agent ID
    coordinatorURL    string                    // Coordinator WebSocket URL
    agentCoreURL      string                    // AgentCore HTTP URL
    listenAddr        string                    // HTTP 监听地址
    
    heartbeatInterval int                      // 心跳间隔(秒)
    reconnectInterval int                      // 重连间隔(秒)
    
    memoryWins     map[string]*MemoryWindow   // 每个聊天室一个 Memory Window
    sessionMgr     *SessionManager             // Session 管理
    wsConn         *websocket.Conn            // WebSocket 连接
    wsMu           sync.Mutex                  // WebSocket 互斥锁
    
    httpServer     *http.Server               // HTTP 服务器
    messageIDs     map[string]bool            // 已处理的消息 ID
    msgIDsMu       sync.Mutex                  // 消息 ID 互斥锁
    
    maxMemorySize     int                     // 最大消息数
    maxMemoryChars    int                     // 最大字符数
}
```

### 3.3 启动流程

```go
func (x *XClient) Start() error {
    // 1. 连接 Coordinator
    if err := x.connectCoordinator(); err != nil {
        return err
    }
    
    // 2. 启动 HTTP 服务器
    go x.startHTTPServer()
    
    // 3. 启动心跳
    go x.keepAlive()
    
    return nil
}
```

### 3.4 连接 Coordinator

```go
func (x *XClient) connectCoordinator() error {
    // 1. 建立 WebSocket 连接
    conn, _, err := websocket.DefaultDialer.Dial(x.coordinatorURL + "/ws/chat", nil)
    if err != nil {
        return err
    }
    x.wsConn = conn
    
    // 2. 注册 Agent ID
    registerMsg := ClientMessage{
        Action: "register",
        Data: map[string]string{
            "agentId": x.agentID,
        },
    }
    x.sendMessage(registerMsg)
    
    // 3. 启动接收循环
    go x.receiveLoop()
    
    return nil
}
```

### 3.5 消息接收循环

```go
func (x *XClient) receiveLoop() {
    for {
        var msg ServerMessage
        if err := x.wsConn.ReadJSON(&msg); err != nil {
            logWarn("x-client", x.agentID, "WebSocket 读取失败，尝试重连")
            x.reconnect()
            return
        }
        x.handleServerMessage(&msg)
    }
}
```

### 3.6 消息处理

```go
func (x *XClient) handleServerMessage(msg *ServerMessage) {
    switch msg.Type {
    case "message":
        // 处理新消息
        x.handleIncomingMessage(&a2aMsg)
        
    case "history":
        // 处理历史消息
        x.syncHistory(&history)
        
    case "error":
        // 处理错误
        logError("x-client", x.agentID, "收到错误", "error", msg.Data)
    }
}
```

### 3.7 消息区分

```go
func (x *XClient) handleIncomingMessage(msg *A2AMessage) {
    // 1. 去重检查
    if x.isMessageProcessed(msg.MsgId) {
        return
    }
    
    // 2. 获取当前聊天室的 Memory Window
    memoryWin := x.getMemoryWindow(msg.ChannelId)
    
    // 3. 检查是否被 @ 提及
    isMentioned := false
    for _, user := range msg.MentionUsers {
        if user == x.agentID {
            isMentioned = true
            break
        }
    }
    
    if !isMentioned {
        // 旁听模式：仅存储消息
        memoryWin.Push(msg.Sender, msg.ContentText)
        return
    }
    
    // 被 @ 唤醒：调用 AgentCore
    contextPrompt := memoryWin.BuildContext(msg.Sender, msg.ContentText)
    go x.wakeupAgentCore(contextPrompt, msg)
}
```

### 3.8 Memory Window

每个聊天室维护一个 Memory Window：

```go
type MemoryWindow struct {
    messages   []MemoryItem    // 消息列表
    maxSize    int             // 最大消息数
    maxChars   int             // 最大字符数
    mu         sync.Mutex
}

type MemoryItem struct {
    Sender string
    Content string
}
```

#### 添加消息

```go
func (m *MemoryWindow) Push(sender, content string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    m.messages = append(m.messages, MemoryItem{Sender: sender, Content: content})
    
    // 裁剪超长内容
    m.trimToSize()
}
```

#### 构建上下文

```go
func (m *MemoryWindow) BuildContext(sender, currentMsg string) string {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    var sb strings.Builder
    for _, item := range m.messages {
        sb.WriteString(fmt.Sprintf("%s: %s\n", item.Sender, item.Content))
    }
    sb.WriteString(fmt.Sprintf("%s: %s\n", sender, currentMsg))
    
    result := sb.String()
    if len(result) > m.maxChars {
        // 截断
        result = result[:m.maxChars] + "...(截断)"
    }
    
    return result
}
```

### 3.9 唤醒 AgentCore

```go
func (x *XClient) wakeupAgentCore(prompt, sessionID string, originalMsg *A2AMessage) {
    reqBody := map[string]string{
        "message":    prompt,
        "session_id": sessionID,
        "sender":     originalMsg.Sender,
    }
    
    resp, err := http.Post(x.agentCoreURL + "/chat", "application/json", bytes.NewBuffer(jsonData))
    if err != nil {
        logError("x-client", x.agentID, "调用 agentcore 失败", "error", err.Error())
        return
    }
    defer resp.Body.Close()
    
    var result struct {
        Reply string `json:"reply"`
    }
    json.NewDecoder(resp.Body).Decode(&result)
    
    // 发送回复到聊天室
    x.sendGroupMessage(result.Reply, originalMsg.ChannelId, nil, originalMsg.MsgId)
}
```

### 3.10 自动重连

```go
func (x *XClient) reconnect() {
    for {
        time.Sleep(time.Duration(x.reconnectInterval) * time.Second)
        logInfo("x-client", x.agentID, "尝试重新连接协调器...")
        if err := x.connectCoordinator(); err == nil {
            logInfo("x-client", x.agentID, "重连成功")
            return
        }
    }
}
```

---

## 4. HTTP API

### 4.1 Skill 回调

**POST** `/skill/send`

发送消息到聊天室（用于 Skill 回调）。

**请求体**：
```json
{
  "channel_id": "room_test_123",
  "content": "这是 Skill 的回复",
  "mention_users": [],
  "reply_to_msg_id": "original-msg-id"
}
```

**响应**：
```json
{
  "status": "sent"
}
```

---

### 4.2 健康检查

**GET** `/health`

**响应**：
```
OK
```

---

## 5. 配置说明

### 5.1 配置文件格式

创建 `config.json` 文件：

```json
{
  "agent_id": "agent_1",
  "coordinator": "ws://localhost:8080/ws/chat",
  "agentcore": "http://localhost:8000",
  "listen": ":8001",
  "heartbeat_interval": 30,
  "reconnect_interval": 5,
  "max_memory_size": 50,
  "max_memory_chars": 2000
}
```

### 5.2 配置项说明

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| agent_id | - | Agent 的唯一标识 (必需) |
| coordinator | - | Coordinator WebSocket 地址 (必需) |
| agentcore | - | AgentCore HTTP 地址 (必需) |
| listen | - | HTTP 监听地址 (必需) |
| heartbeat_interval | 30 | 心跳间隔(秒) |
| reconnect_interval | 5 | 重连间隔(秒) |
| max_memory_size | 50 | Memory Window 最大消息数 |
| max_memory_chars | 2000 | Memory Window 最大字符数 |

### 5.3 命令行参数

```bash
./bin/x-client \
    --agent-id=agent_1 \
    --coordinator=ws://localhost:8080/ws/chat \
    --agentcore=http://localhost:8000 \
    --listen=:8001
```

---

## 6. 目录结构

```
x-client/
├── bin/
│   └── x-client              # 编译后的可执行文件
├── main.go                   # 程序入口
├── config.go                 # 配置加载
├── logging.go                # 日志工具
├── session.go                # Session 管理
├── memory.go                 # Memory Window 实现
└── go.mod
```

---

## 7. 消息协议

### 7.1 发送消息到 Coordinator

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

#### 发言
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
    "contentText": "大家好"
  }
}
```

#### 心跳
```json
{
  "action": "heartbeat"
}
```

### 7.2 接收消息

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
    "contentText": "@agent_2 你好",
    "timestamp": 1234567890
  }
}
```

#### 历史消息
```json
{
  "type": "history",
  "data": [...]
}
```

---

## 8. 错误处理

### 8.1 常见错误

| 错误 | 说明 | 处理方式 |
|------|------|----------|
| WebSocket 连接失败 | Coordinator 未启动 | 自动重连 |
| AgentCore 调用失败 | AgentCore 异常 | 记录日志 |
| 消息发送失败 | WebSocket 断开 | 重连后重试 |
| JSON 解析失败 | 消息格式错误 | 记录日志，跳过 |

### 8.2 日志级别

- **INFO**: 正常流程日志
- **WARN**: 警告（重连、丢弃消息等）
- **ERROR**: 错误（调用失败等）

---

## 9. 性能考虑

### 9.1 消息去重
- 维护最近 100 条消息 ID
- 超过 100 条时清理一半

### 9.2 上下文裁剪
- 超过 `maxMemorySize` 条消息时裁剪旧消息
- 超过 `maxMemoryChars` 字符时截断

### 9.3 并发安全
- 使用 sync.Mutex 保护共享状态
- 异步处理 AgentCore 调用
