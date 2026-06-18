# x-client - 系统架构文档

## 1. 项目概述

### 1.1 项目目标

构建一个支持多智能体（Agent）在群聊环境中进行自主协作的沙箱系统。用户可以在聊天室内与多个 AI Agent 进行对话，Agent 之间可以通过 @ 提及互相唤醒进行协作。

### 1.2 架构版本说明

| 版本 | 通信方式 | 存储 | 状态 |
|------|----------|------|------|
| **v1 (ws-based)** | 纯 WebSocket | SQLite | 已废弃 |
| **v2 (http-based)** | HTTP 轮询 + WebSocket | MySQL | **当前使用** |

### 1.3 核心能力

- **群聊管理**：创建、加入、删除聊天室
- **消息广播**：支持 @ 提及特定 Agent
- **发言控制**：基于令牌锁的发言冲突解决机制
- **历史消息**：持久化存储聊天记录，支持历史消息加载
- **Agent 协作**：Agent 可以通过 @ 互相唤醒进行协作
- **分布式支持**：Agent 通过 HTTP 轮询获取消息，支持水平扩展

---

## 2. 系统架构

### 2.1 组件架构图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          用户界面层                                      │
│                    (Browser / ui-test)                                  │
│   ┌─────────────────────────────────────────────────────────────────┐   │
│   │  HTML/CSS/JS 前端页面，提供聊天室管理、消息发送、@提及等功能       │   │
│   └─────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
           ┌────────────────────────┼────────────────────────┐
           ▼                        ▼                        ▼
    ┌─────────────┐         ┌─────────────┐         ┌─────────────┐
    │ HTTP API    │         │ WebSocket   │         │ 静态资源    │
    │ /api/*      │         │ /ws/user    │         │ /ui-test/   │
    │             │         │ /ws/chat    │         │             │
    └─────────────┘         └─────────────┘         └─────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                   Coordinator HTTP (:8080)                              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  │
│  │  HTTP API   │  │ WebSocket   │  │ 发言控制    │  │ 消息路由    │  │
│  │  Handler    │  │  Manager    │  │ (Speaker    │  │ (Message    │  │
│  │             │  │             │  │  Lock)      │  │  Router)    │  │
│  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘  │
│  ┌─────────────────────────────────────────────────────────────────┐  │
│  │                         Storage (MySQL)                         │  │
│  │  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐   │  │
│  │  │ agents  │ │  rooms  │ │ members │ │messages │ │locks    │   │  │
│  │  │users    │ │delivery │ │sessions │ │         │ │         │   │  │
│  │  └─────────┘ └─────────┘ └─────────┘ └─────────┘ └─────────┘   │  │
│  └─────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                    ┌───────────────┴───────────────┐
                    ▼                               ▼
        ┌───────────────────────┐      ┌───────────────────────┐
        │   x-client-http       │      │   x-client-http       │
        │   (:8001, agent_1)    │      │   (:8003, agent_2)    │
        │  ┌─────────────┐      │      │  ┌─────────────┐      │
        │  │ HTTP Poll   │      │      │  │ HTTP Poll   │      │
        │  │ (消息轮询)   │      │      │  │ (消息轮询)   │      │
        │  ├─────────────┤      │      │  ├─────────────┤      │
        │  │ Memory      │      │      │  │ Memory      │      │
        │  │ Window      │      │      │  │ Window      │      │
        │  │ (上下文管理) │      │      │  │ (上下文管理) │      │
        │  ├─────────────┤      │      │  ├─────────────┤      │
        │  │ HTTP Server │      │      │  │ HTTP Server │      │
        │  │ (回调接口)   │      │      │  │ (回调接口)   │      │
        │  └─────────────┘      │      │  └─────────────┘      │
        └─────────────┬─────────┘      └─────────────┬─────────┘
                      │                              │
                      └───────────────┬──────────────┘
                                      ▼
                    ┌───────────────────────────────┐
                    │     AgentCore Mock            │
                    │     (:8000, :8002...)         │
                    │  ┌─────────────────────────┐   │
                    │  │ Chat Handler            │   │
                    │  │ - 模拟 AI 响应          │   │
                    │  │ - SSE 流式响应          │   │
                    │  │ - 支持上下文感知        │   │
                    │  └─────────────────────────┘   │
                    └───────────────────────────────┘
```

### 2.2 组件职责

#### 2.2.1 Coordinator HTTP

| 模块 | 职责 |
|------|------|
| **HTTP API Handler** | 处理 Agent 和 User 的 HTTP 请求，包括注册、登录、聊天室管理、消息发送 |
| **WebSocket Manager** | 管理用户的 WebSocket 连接，支持多 Tab 登录，推送实时消息 |
| **发言控制 (Speaker Lock)** | 基于内存 + 数据库双重保证的发言锁机制，防止多 Agent 同时发言 |
| **消息路由 (Message Router)** | 消息存储、投递状态管理、通知轮询 |
| **Storage** | MySQL 数据库操作，包括 Agent、Room、Member、Message 等数据的 CRUD |

#### 2.2.2 x-client-http

| 模块 | 职责 |
|------|------|
| **HTTP Poll** | 定期轮询 Coordinator 获取新消息，支持去重 |
| **Memory Window** | 维护每个聊天室的消息历史，构建上下文提示 |
| **HTTP Server** | 提供健康检查、状态查询、Skill 回调接口 |
| **AgentCore 调用** | 被 @ 唤醒时调用 AgentCore 生成响应，支持 SSE 流式响应 |

#### 2.2.3 AgentCore Mock

| 模块 | 职责 |
|------|------|
| **Chat Handler** | 模拟 AI 响应生成，支持普通 HTTP 和 SSE 流式响应 |

#### 2.2.4 ui-test

| 模块 | 职责 |
|------|------|
| **HTML/CSS/JS** | 测试前端页面，提供聊天室管理和消息发送界面 |
| **manager** | 服务管理后端，启动/停止各组件（可选） |

---

## 3. 组件间交互逻辑

### 3.1 Agent 注册流程

```
x-client-http                    Coordinator                    MySQL
     │                               │                             │
     │ POST /api/agent/register      │                             │
     │ {agent_id, endpoint}          │                             │
     ├──────────────────────────────►│                             │
     │                               │ INSERT/UPDATE agents        │
     │                               │ ON DUPLICATE KEY UPDATE     │
     │                               ├────────────────────────────►│
     │                               │                             │
     │ 200 OK {success: true}        │                             │
     ├───────────────────────────────│                             │
```

### 3.2 用户加入聊天室流程

```
User Browser                    Coordinator                    MySQL
     │                               │                             │
     │ POST /api/room/join          │                             │
     │ {room_id, member_id, type}   │                             │
     ├──────────────────────────────►│                             │
     │                               │ 检查会话状态                 │
     │                               │ CheckAndCreateUserRoomSession│
     │                               ├────────────────────────────►│
     │                               │                             │
     │                               │ 添加成员                     │
     │                               │ AddMember                   │
     │                               ├────────────────────────────►│
     │                               │                             │
     │                               │ 获取历史消息                 │
     │                               │ GetRecentMessages           │
     │                               ├────────────────────────────►│
     │                               │                             │
     │ 200 OK {room, history, session_id}                         │
     ├───────────────────────────────│                             │
     │                               │                             │
     │ 建立 WebSocket 连接           │                             │
     │ ws://host/ws/user?session_id= │                             │
     ├──────────────────────────────►│                             │
     │                               │ 验证会话，更新连接状态        │
     │                               │ UpdateUserRoomSession       │
     │                               ├────────────────────────────►│
```

### 3.3 消息发送流程（用户 → Coordinator）

```
User Browser                    Coordinator                    MySQL
     │                               │                             │
     │ POST /api/message            │                             │
     │ {room_id, sender, content}   │                             │
     ├──────────────────────────────►│                             │
     │                               │ 验证聊天室和成员             │
     │                               │ GetRoom, IsMemberInRoom     │
     │                               ├────────────────────────────►│
     │                               │                             │
     │                               │ 获取发言锁（非@消息）        │
     │                               │ TryAcquireLock              │
     │                               ├────────────────────────────►│
     │                               │                             │
     │                               │ 保存消息                     │
     │                               │ SaveMessage                 │
     │                               ├────────────────────────────►│
     │                               │                             │
     │ 200 OK {msg_id}              │                             │
     ├───────────────────────────────│                             │
     │                               │                             │
     │ WebSocket 推送消息            │                             │
     │ (notificationPump 轮询)       │                             │
```

### 3.4 消息处理流程（Agent 轮询 → 响应）

```
x-client-http                    Coordinator                    AgentCore
     │                               │                             │
     │ GET /api/poll?agent_id=       │                             │
     │ &since=xxx                    │                             │
     ├──────────────────────────────►│                             │
     │                               │ 查询未投递消息               │
     │                               │ PollMessages                │
     │                               ├────────────────────────────►│
     │                               │                             │
     │ 200 OK {messages}             │                             │
     ├───────────────────────────────│                             │
     │                               │                             │
     │ 检查是否被 @                   │                             │
     │ 是 → 唤醒 AgentCore           │                             │
     ├─────────────────────────────────────────────────────────────►│
     │                               │                             │
     │ POST /chat/stream            │                             │
     │ {message, session_id}        │                             │
     │←─── SSE 流式响应 ─────────────│                             │
     │                               │                             │
     │ POST /api/message (回复)      │                             │
     ├──────────────────────────────►│                             │
```

### 3.5 消息投递流程（用户 WebSocket）

```
Coordinator (notificationPump)    MySQL                    User Browser
     │                               │                             │
     │ GetPendingNotifications       │                             │
     ├──────────────────────────────►│                             │
     │                               │ 查询待通知消息              │
     │                               │ (sender != user,            │
     │                               │  notified_at IS NULL)       │
     │                               │                             │
     │ 更新 notified_at              │                             │
     ├──────────────────────────────►│                             │
     │                               │                             │
     │ WebSocket 推送                │                             │
     ├─────────────────────────────────────────────────────────────►│
     │ {"type": "message", "data": ...}                            │
```

---

## 4. 模块接口详细说明

### 4.1 Coordinator HTTP API

#### 4.1.1 Agent API

| 方法 | 路径 | 请求体 | 响应 | 说明 |
|------|------|--------|------|------|
| POST | `/api/agent/register` | `{agent_id, endpoint}` | `{success, message}` | Agent 注册或更新 |
| POST | `/api/agent/heartbeat` | `{agent_id}` | `{status: "ok"}` | 更新心跳时间 |
| GET | `/api/poll` | Query: `agent_id`, `since`, `room_id`, `limit` | `{messages, next_since}` | Agent 轮询消息 |

**POST /api/agent/register**

请求：
```json
{
  "agent_id": "agent_1",
  "endpoint": "http://localhost:8001"
}
```

响应：
```json
{
  "success": true,
  "message": "注册成功"
}
```

**GET /api/poll**

请求：
```
GET /api/poll?agent_id=agent_1&since=1234567890&limit=50
```

响应：
```json
{
  "messages": [
    {
      "msg_id": "uuid-xxx",
      "room_id": "room_xxx",
      "sender_id": "user_1",
      "sender_type": "user",
      "content": "@agent_1 你好",
      "mention_users": ["agent_1"],
      "intent": "REQUEST",
      "created_at": 1234567890
    }
  ],
  "next_since": 1234567890
}
```

#### 4.1.2 User API

| 方法 | 路径 | 请求体 | 响应 | 说明 |
|------|------|--------|------|------|
| POST | `/api/user/register` | `{username, password, nickname}` | `{success, user}` | 用户注册 |
| POST | `/api/user/login` | `{username, password}` | `{success, user}` | 用户登录（不存在则自动注册） |
| GET | `/api/user/get` | Query: `user_id` | `{success, user}` | 获取用户信息 |

#### 4.1.3 Room API

| 方法 | 路径 | 请求体 | 响应 | 说明 |
|------|------|--------|------|------|
| GET | `/api/rooms` | - | `{success, rooms}` | 获取所有聊天室 |
| POST | `/api/room/create` | `{name, description, members, created_by}` | `{success, room_id}` | 创建聊天室 |
| POST | `/api/room/join` | `{room_id, member_id, member_type}` | `{success, room, history, session_id}` | 加入聊天室 |
| POST | `/api/room/leave` | `{room_id, member_id}` | `{success}` | 离开聊天室 |
| DELETE | `/api/room/{room_id}/leave/{member_id}` | - | `{success}` | 离开聊天室（路径参数） |
| GET | `/api/room/history` | Query: `room_id`, `count` | `{success, messages}` | 获取历史消息 |
| GET | `/api/room/members` | Query: `room_id` | `{success, members}` | 获取成员列表 |
| GET | `/api/room/{room_id}/members` | - | `{success, members}` | 获取成员列表（路径参数） |

**POST /api/room/create**

请求：
```json
{
  "name": "测试聊天室",
  "description": "用于测试的聊天室",
  "members": ["agent_1", "agent_2"],
  "created_by": "admin"
}
```

响应：
```json
{
  "success": true,
  "room_id": "room_1234567890"
}
```

**POST /api/room/join**

请求：
```json
{
  "room_id": "room_xxx",
  "member_id": "user_1",
  "member_type": "user"
}
```

响应：
```json
{
  "success": true,
  "room": {
    "room_id": "room_xxx",
    "name": "测试聊天室"
  },
  "history": [...],
  "session_id": 123
}
```

#### 4.1.4 Message API

| 方法 | 路径 | 请求体 | 响应 | 说明 |
|------|------|--------|------|------|
| POST | `/api/message` | `{room_id, sender_id, sender_type, content, target_id, mention_users, intent, reply_to_msg_id}` | `{success, msg_id}` | 发送消息 |

**POST /api/message**

请求：
```json
{
  "room_id": "room_xxx",
  "sender_id": "user_1",
  "sender_type": "user",
  "content": "@agent_1 你好",
  "target_id": "ALL",
  "mention_users": ["agent_1"],
  "intent": "REQUEST",
  "reply_to_msg_id": ""
}
```

响应：
```json
{
  "success": true,
  "msg_id": "uuid-xxx"
}
```

#### 4.1.5 WebSocket API

| 路径 | 参数 | 说明 |
|------|------|------|
| `/ws/user` | `user_id`, `room_id`, `session_id` | 用户 WebSocket 连接 |
| `/ws/chat` | `user_id`, `room_id`, `session_id` | 聊天室 WebSocket 连接 |

WebSocket 消息格式：

```json
{
  "type": "message",
  "data": {
    "msgId": "uuid-xxx",
    "roomId": "room_xxx",
    "senderId": "user_1",
    "content": "消息内容",
    "mentionUsers": ["agent_1"],
    "intent": "INFORM",
    "createdAt": 1234567890
  }
}
```

### 4.2 x-client-http API

| 方法 | 路径 | 请求体 | 响应 | 说明 |
|------|------|--------|------|------|
| GET | `/health` | - | `"OK"` | 健康检查 |
| GET | `/status` | - | `{agent_id, status, poll_interval, message_count, room_count}` | 获取 Agent 状态 |
| POST | `/skill/callback` | `{room_id, sender, content, msg_id, intent}` | `{status}` | Skill 回调接口 |

**POST /skill/callback**

请求：
```json
{
  "room_id": "room_xxx",
  "sender": "user_1",
  "content": "执行技能",
  "msg_id": "uuid-xxx",
  "intent": "REQUEST"
}
```

响应：
```json
{
  "status": "received"
}
```

### 4.3 AgentCore Mock API

| 方法 | 路径 | 请求体 | 响应 | 说明 |
|------|------|--------|------|------|
| GET | `/health` | - | `"OK"` | 健康检查 |
| POST | `/chat` | `{message, session_id, sender}` | `{reply}` | 普通聊天接口 |
| POST | `/chat/stream` | `{message, session_id, sender}` | SSE 流式响应 | SSE 流式聊天接口 |

---

## 5. 核心机制

### 5.1 发言控制 (Speaker Lock)

#### 5.1.1 设计目标

防止多个 Agent 同时发言造成消息混乱。

#### 5.1.2 实现机制

```
┌─────────────────────────────────────────────────────────────┐
│                    Speaker Lock 机制                        │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   内存层 (speeches map)          数据库层 (speaker_locks)    │
│   ┌──────────────────────┐      ┌──────────────────────┐    │
│   │ room_id → SpeakerLock │      │ room_id (UNIQUE)     │    │
│   │   - HolderID         │      │ holder_id            │    │
│   │   - ExpiresAt        │      │ holder_type          │    │
│   └──────────────────────┘      │ expires_at           │    │
│                                 └──────────────────────┘    │
│              │                           │                  │
│              ▼                           ▼                  │
│         TryAcquireLock              INSERT/UPDATE           │
│         1. 检查内存锁               ON DUPLICATE KEY        │
│         2. 更新数据库锁             UPDATE                  │
│         3. 更新内存锁                                        │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

#### 5.1.3 锁规则

| 场景 | 是否需要锁 | 释放时机 |
|------|-----------|----------|
| 用户发送普通消息 | 是 | 延迟释放（默认 2 秒） |
| 用户发送 @ 消息 | 否 | - |
| Agent 发送消息 | 否 | - |

#### 5.1.4 冲突处理

- 获取锁失败时返回 `409 Conflict`
- 响应体包含当前发言者信息

### 5.2 上下文管理 (Memory Window)

#### 5.2.1 设计目标

每个 Agent 维护对话历史，提供上下文感知能力。

#### 5.2.2 数据结构

```go
type MemoryWindow struct {
    maxSize   int           // 最大消息数（默认 50）
    maxChars  int           // 最大字符数（默认 2000）
    messages  []string      // 消息队列 ["[sender]: content", ...]
    totalLen  int           // 当前总字符数
}
```

#### 5.2.3 上下文构建

```go
func (m *MemoryWindow) BuildContext(sender, currentMsg string) string {
    // 输出格式：
    // [agent_1]: 消息1
    // [user_1]: 消息2
    // [agent_1]: 消息3
    // [user_1]: 当前消息
    // []: 
}
```

#### 5.2.4 裁剪策略

1. **数量裁剪**：超过 `maxSize` 条时，移除最早的消息
2. **字符裁剪**：超过 `maxChars` 时，移除最早的消息

### 5.3 消息去重

#### 5.3.1 设计目标

防止同一消息被重复处理。

#### 5.3.2 实现机制

```go
type XClient struct {
    messageIDs map[string]bool  // msg_id → true
    msgIDsMu   sync.Mutex
}

func (x *XClient) isMessageProcessed(msgId string) bool {
    // 检查消息是否已处理
    // 如果未处理，添加到 map 中
    // 超过 1000 条时清理一半
}
```

### 5.4 多 Tab 登录支持

#### 5.4.1 设计目标

支持同一用户在多个浏览器标签页同时登录。

#### 5.4.2 实现机制

```
┌─────────────────────────────────────────────────────────────┐
│                   多 Tab 登录机制                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   用户连接管理 (userConns)                                   │
│   ┌─────────────────────────────────────────────────────┐   │
│   │ connection_id → UserConn                             │   │
│   │   - UserID (同一用户多个连接)                         │   │
│   │   - ConnectionID (唯一连接标识)                       │   │
│   │   - Conn (WebSocket 连接)                            │   │
│   │   - Rooms (订阅的聊天室)                             │   │
│   └─────────────────────────────────────────────────────┘   │
│                                                             │
│   用户房间会话 (user_room_sessions 表)                       │
│   ┌─────────────────────────────────────────────────────┐   │
│   │ user_id + room_id (UNIQUE)                           │   │
│   │ connection_id (当前连接)                              │   │
│   │ ws_established (WS 是否已建立)                        │   │
│   │ connected_at, last_active_at                         │   │
│   └─────────────────────────────────────────────────────┘   │
│                                                             │
│   通知机制 (notifySender)                                   │
│   - 根据 user_id 查找所有连接                               │
│   - 向每个连接发送消息                                       │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

#### 5.4.3 会话验证流程

1. 用户调用 `/api/room/join` 获取 `session_id`
2. 前端使用 `session_id` 建立 WebSocket 连接
3. Server 验证 `session_id` 并更新连接状态
4. 消息通过 `notificationPump` 推送到所有连接

---

## 6. 存储设计

### 6.1 数据库表结构

#### 6.1.1 agents 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键（自增） |
| agent_id | VARCHAR(64) | Agent 唯一标识（UNIQUE） |
| endpoint | VARCHAR(512) | Agent HTTP 访问地址 |
| status | VARCHAR(32) | ONLINE/OFFLINE |
| last_heartbeat | DATETIME | 最后心跳时间 |
| created_at | DATETIME | 创建时间 |

#### 6.1.2 rooms 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键（自增） |
| room_id | VARCHAR(64) | 聊天室唯一标识（UNIQUE） |
| name | VARCHAR(128) | 聊天室名称 |
| description | TEXT | 聊天室描述 |
| created_by | VARCHAR(64) | 创建者 |
| created_at | DATETIME | 创建时间 |

#### 6.1.3 members 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键（自增） |
| room_id | VARCHAR(64) | 聊天室 ID |
| member_id | VARCHAR(64) | 成员 ID |
| member_type | VARCHAR(32) | agent / user |
| joined_at | DATETIME | 加入时间 |
| left_at | DATETIME | 离开时间（NULL 表示活跃） |
| is_active | BOOLEAN | 是否活跃 |

#### 6.1.4 users 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键（自增） |
| user_id | VARCHAR(64) | 用户唯一标识（UNIQUE） |
| username | VARCHAR(64) | 用户名 |
| password_hash | VARCHAR(256) | 密码哈希 |
| email | VARCHAR(128) | 邮箱 |
| status | VARCHAR(32) | ONLINE/OFFLINE |
| last_login | DATETIME | 最后登录时间 |
| created_at | DATETIME | 创建时间 |

#### 6.1.5 messages 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键（自增） |
| msg_id | VARCHAR(64) | 消息唯一标识（UNIQUE） |
| room_id | VARCHAR(64) | 聊天室 ID |
| sender_id | VARCHAR(64) | 发送者 ID |
| sender_type | VARCHAR(32) | agent / user / system |
| target_id | VARCHAR(64) | 目标 ID（ALL 表示广播） |
| target_type | VARCHAR(32) | BROADCAST / DIRECT |
| mention_users | TEXT | @ 提及的用户（JSON 数组） |
| content | TEXT | 消息内容 |
| intent | VARCHAR(32) | INFORM / REQUEST / RESPONSE / SYSTEM |
| status | VARCHAR(32) | PENDING / DELIVERED / READ |
| reply_to_msg_id | VARCHAR(64) | 回复的消息 ID |
| created_at | DATETIME | 创建时间 |

#### 6.1.6 speaker_locks 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键（自增） |
| room_id | VARCHAR(64) | 聊天室 ID（UNIQUE） |
| holder_id | VARCHAR(64) | 锁持有者 ID |
| holder_type | VARCHAR(32) | agent / user |
| acquired_at | DATETIME | 获取时间 |
| expires_at | DATETIME | 过期时间 |

#### 6.1.7 message_delivery 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键（自增） |
| msg_id | VARCHAR(64) | 消息 ID |
| recipient_id | VARCHAR(64) | 接收者 ID |
| delivered_at | DATETIME | 已被 poll 拉取的时间 |
| notified_at | DATETIME | 已通过 WebSocket 通知的时间 |

#### 6.1.8 user_room_sessions 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键（自增） |
| user_id | VARCHAR(64) | 用户 ID |
| room_id | VARCHAR(64) | 聊天室 ID |
| connection_id | VARCHAR(255) | 连接 ID |
| ws_established | BOOLEAN | WebSocket 是否已建立 |
| connected_at | DATETIME | 连接时间 |
| last_active_at | DATETIME | 最后活跃时间 |

### 6.2 索引设计

| 表名 | 索引 | 用途 |
|------|------|------|
| agents | idx_agent_id | Agent 查找 |
| agents | idx_status_heartbeat | 离线检测 |
| rooms | idx_room_id | 聊天室查找 |
| members | uk_room_member | 唯一性约束 |
| members | idx_member_id | 成员查找 |
| messages | idx_room_created | 按房间查询消息 |
| messages | idx_target_status | 按目标查询消息 |
| speaker_locks | idx_expires | 过期锁清理 |
| message_delivery | uk_msg_recipient | 唯一性约束 |
| message_delivery | idx_recipient | 按接收者查询 |
| user_room_sessions | uk_user_room | 唯一性约束 |

### 6.3 存储目录说明

| 目录 | 说明 | 使用情况 |
|------|------|----------|
| `http-based/coordinator-http/storage.go` | HTTP 版本的存储实现（MySQL） | **当前使用** |
| `storage/` | 旧版存储抽象（SQLite/MySQL/Redis） | 仅 ws-based 使用，已废弃 |

---

## 7. 部署架构

### 7.1 单机部署

```
┌─────────────────────────────────────────────────────────┐
│                      localhost                          │
│                                                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐     │
│  │ Coordinator │  │ x-client-1  │  │ x-client-2  │     │
│  │ HTTP :8080  │  │ :8001       │  │ :8003       │     │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘     │
│         │                │                │             │
│         └────────────────┼────────────────┘             │
│                          ▼                              │
│  ┌─────────────────────────────────────────────┐       │
│  │              MySQL Database                  │       │
│  │              (xclient)                       │       │
│  └─────────────────────────────────────────────┘       │
│                                                         │
│  ┌─────────────┐  ┌─────────────┐                      │
│  │ AgentCore   │  │ ui-test     │                      │
│  │ Mock :8000  │  │ manager:9000│                      │
│  └─────────────┘  └─────────────┘                      │
└─────────────────────────────────────────────────────────┘
```

### 7.2 分布式部署

```
┌──────────────────────────────────────────────────────────────────┐
│                          负载均衡器                                │
│                         (Nginx)                                   │
│                                                                  │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │
│  │ Coordinator │  │ Coordinator │  │ Coordinator │              │
│  │ Node 1      │  │ Node 2      │  │ Node 3      │              │
│  │ :8080       │  │ :8080       │  │ :8080       │              │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘              │
│         │                │                │                      │
│         └────────────────┼────────────────┘                      │
│                          ▼                                       │
│  ┌───────────────────────────────────────────────────────────┐   │
│  │                    MySQL (主从复制)                        │   │
│  │  ┌───────────┐  ┌───────────┐  ┌───────────┐             │   │
│  │  │ Master    │  │ Slave 1   │  │ Slave 2   │             │   │
│  │  └───────────┘  └───────────┘  └───────────┘             │   │
│  └───────────────────────────────────────────────────────────┘   │
│                                                                  │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │
│  │ x-client    │  │ x-client    │  │ x-client    │              │
│  │ Agent 1     │  │ Agent 2     │  │ Agent N     │              │
│  │ :8001       │  │ :8003       │  │ :800N       │              │
│  └─────────────┘  └─────────────┘  └─────────────┘              │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

### 7.3 启动顺序

1. **MySQL** - 数据库服务
2. **Coordinator HTTP** - 消息协调器
3. **AgentCore Mock** - Mock AI 服务（每个 Agent 一个）
4. **x-client-http** - Agent 客户端（每个 Agent 一个）
5. **ui-test/manager** - 测试管理器（可选）

---

## 8. 配置说明

### 8.1 Coordinator HTTP 配置

```json
{
  "listen_addr": ":8080",
  "db_host": "localhost",
  "db_port": 3306,
  "db_user": "root",
  "db_password": "",
  "db_name": "xclient",
  "speaker_lock_timeout_ms": 2000,
  "heartbeat_timeout_sec": 60,
  "message_retention_days": 7,
  "poll_batch_size": 50
}
```

### 8.2 x-client-http 配置

```json
{
  "agent_id": "agent_1",
  "coordinator_url": "http://localhost:8080",
  "agentcore_url": "http://localhost:8000",
  "listen_addr": ":8001",
  "endpoint": "http://localhost:8001",
  "poll_interval": 5,
  "poll_batch_size": 50,
  "heartbeat_interval": 30,
  "max_memory_size": 50,
  "max_memory_chars": 2000
}
```

---

## 9. 监控与运维

### 9.1 健康检查端点

| 组件 | 端点 |
|------|------|
| Coordinator HTTP | `GET /health` |
| x-client-http | `GET /health` |
| AgentCore Mock | `GET /health` |

### 9.2 状态端点

| 组件 | 端点 | 说明 |
|------|------|------|
| x-client-http | `GET /status` | 返回 Agent 运行状态 |

### 9.3 日志

- 格式：文本日志（stdout）
- 级别：INFO, WARN, ERROR
- 输出：控制台输出

### 9.4 定期清理任务

Coordinator 内部启动后台协程定期执行：
- **清理过期发言锁**：每 5 分钟清理一次
- **检测离线 Agent**：每 5 分钟检查一次（心跳超时 60 秒）
- **清理旧消息**：每 5 分钟清理一次（保留 7 天）

---

## 10. 安全性考虑

### 10.1 跨域

- WebSocket `CheckOrigin: true`（生产环境应限制来源）
- 静态资源允许所有来源

### 10.2 消息去重

- x-client 维护最近 1000 条消息 ID
- 超过 1000 条时清理一半

### 10.3 会话验证

- WebSocket 连接必须携带有效的 `session_id`
- `session_id` 通过 `/api/room/join` 获取并验证

### 10.4 输入验证

- 所有 API 请求体进行 JSON 解析和字段验证
- `mention_users` 中引用的 Agent 需要验证存在且在聊天室中

---

## 11. 扩展建议

### 11.1 水平扩展

- 部署多个 Coordinator 实例
- 使用 MySQL 主从复制
- 使用负载均衡器分发请求
- Agent 通过 HTTP 轮询天然支持分布式

### 11.2 功能扩展

- 支持更多 AI 提供商（OpenAI, Anthropic）
- 添加消息加密
- 支持文件/图片消息
- 添加权限控制系统
- 添加消息搜索功能
- 添加消息编辑/删除功能

### 11.3 架构优化

- 引入 Redis 缓存热点数据
- 使用消息队列（Kafka/RabbitMQ）解耦消息处理
- 添加 Prometheus 指标采集
- 添加分布式追踪（Jaeger）
