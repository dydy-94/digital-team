# X-Client HTTP 技术文档

> X-Client 是 Clawith A2A 架构中 Agent 的代理，负责 Agent 与 Coordinator 之间的通信，支持任务委托、权限管理、文件传输等功能。

## 目录

1. [项目概述](#1-项目概述)
2. [系统架构](#2-系统架构)
3. [核心能力](#3-核心能力)
4. [经典业务流程](#4-经典业务流程)
5. [API 文档](#5-api-文档)
6. [数据库设计](#6-数据库设计)
7. [配置说明](#7-配置说明)
8. [目录结构](#8-目录结构)

---

## 1. 项目概述

### 1.1 项目组成

| 组件 | 路径 | 说明 |
|------|------|------|
| Coordinator HTTP | `http-based/coordinator-http/` | 消息路由中枢，提供 Agent 注册、消息转发、任务管理、权限控制等 API |
| X-Client HTTP | `http-based/x-client-http/` | Agent 代理客户端，负责与 Coordinator 通信、管理本地工作区 |

### 1.2 技术栈

| 组件 | 技术 |
|------|------|
| 语言 | Go 1.21+ |
| HTTP 框架 | gorilla/mux |
| WebSocket | gorilla/websocket |
| 数据库 | MySQL 8.0+ |
| 对象存储 | S3 / MinIO |
| AWS SDK | aws-sdk-go-v2 |

### 1.3 主要特性

- **Agent 注册与管理**: 支持多 Agent 通过心跳机制保持在线状态
- **消息路由**: 基于 Room 的消息广播和 @ 提及通知
- **任务委托**: `/delegate` 命令支持创建任务并分配给指定 Agent
- **权限控制**: 基于 Agent 级别的权限管理，支持工具级权限控制
- **文件传输**: 基于 S3 Presigned URL 的文件上传下载
- **工作区管理**: 本地工作区文件管理，支持报告读取

---

## 2. 系统架构

### 2.1 整体架构

```
┌─────────────────────────────────────────────────────────────────────┐
│                         X-Client HTTP                                │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────────────────┐  │
│  │  XClient    │    │ Permission │    │   WorkspaceManager       │  │
│  │  - 注册     │    │   Cache    │    │   - 文件上传/下载        │  │
│  │  - 轮询     │    │   - 缓存   │    │   - 报告读取            │  │
│  │  - 消息处理 │    │   - 清理   │    │   - 工作区管理          │  │
│  │  - 命令解析 │    └─────────────┘    └─────────────────────────┘  │
│  └──────┬──────┘                                                      │
│         │ HTTP / SSE                                                  │
└─────────┼───────────────────────────────────────────────────────────┘
          │
          ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      Coordinator HTTP                                │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────────────────┐  │
│  │  Storage    │    │  Handler   │    │   S3Client              │  │
│  │  - MySQL    │    │  - Agent   │    │   - Presigned URL      │  │
│  │  - 消息     │    │  - Room    │    │   - Bucket 管理        │  │
│  │  - 任务     │    │  - Task    │    │   - 文件操作           │  │
│  │  - 权限     │    │  - File    │    └─────────────────────────┘  │
│  │  - 文件     │    │  - WebSocket│                                  │
│  └─────────────┘    └─────────────┘                                   │
│                           │                                          │
│                           ▼                                          │
│                    ┌─────────────┐                                   │
│                    │   MySQL    │◄── tasks, messages, permissions   │
│                    └─────────────┘                                   │
│                                                                     │
│                           │                                          │
│                           ▼                                          │
│                    ┌─────────────┐                                   │
│                    │  MinIO / S3 │◄── file_transfers                │
│                    └─────────────┘                                   │
└─────────────────────────────────────────────────────────────────────┘
```

### 2.2 消息流转

```
AgentCore A ──► X-Client A ──► Coordinator ──► 数据库 (messages 表)
                                       │
                    ┌──────────────────┼──────────────────┐
                    │                  │                  │
                    ▼                  ▼                  ▼
             X-Client B          WebSocket 用户      其他 Agent
            (轮询获取)        (notificationPump)   (轮询获取)
                    │                  │                  │
                    ▼                  ▼                  ▼
              AgentCore B          前端显示           其他 AgentCore
```

**说明：**
- Coordinator 是消息中枢，负责接收消息并存储到数据库
- X-Client 和 Agent 通过**轮询** (Poll API) 获取新消息，不是被推送
- WebSocket 用户通过 **notificationPump** 轮询机制接收消息推送
- 消息以 Room 为单位进行隔离，Room 内所有成员都能看到消息

**通信模式：**
| 方向 | 方式 |
|------|------|
| X-Client → Coordinator | HTTP API (同步) |
| Coordinator → X-Client | X-Client 主动轮询 |
| Coordinator → WebSocket | notificationPump 轮询推送 |

---

## 3. 核心能力

### 3.1 Agent 管理

| 能力 | 说明 |
|------|------|
| 注册 | Agent 启动时向 Coordinator 注册，提供 endpoint |
| 心跳 | 定期发送心跳，维持在线状态 |
| 离线检测 | Coordinator 检测离线 Agent（默认 60s 无心跳视为离线）|
| 轮询消息 | Agent 定时轮询 Coordinator 获取新消息 |

### 3.2 消息系统

| 能力 | 说明 |
|------|------|
| Room 消息 | 基于聊天室的消息广播 |
| @ 提及 | 支持 @ 指定 Agent，被 @ 的 Agent 会被唤醒 |
| Intent 路由 | 支持 DELEGATE、QUERY、RESPONSE、FILE 等 Intent 类型 |
| 历史消息 | 支持获取聊天室历史消息 |

### 3.3 任务委托 (DELEGATE Intent)

| 能力 | 说明 |
|------|------|
| 命令格式 | `/delegate <标题> to <agent_id> [with focus [ ] <关注点1>, [ ] <关注点2>]` |
| 任务创建 | 在 Coordinator 创建 Task 记录 |
| 关注点 | 自动创建 FocusItem 记录 |
| 消息绑定 | 消息与 Task 关联（task_id 字段）|

**示例命令（用户触发）：**
```
/delegate 设计登录页面 to agent-001 with focus [ ] 设计 UI, [ ] 实现后端
```

**Agent 自主委托（Agent → Agent）：**

x-client 提供 `/skill/delegate` 接口，Coordinator 可以调用此接口让 Agent 自主接收任务：

| 接口 | 方法 | 说明 |
|------|------|------|
| `/skill/delegate` | POST | Coordinator 委派任务给 Agent |
| `/skill/send` | POST | Agent 主动发送消息 |

**流程：**
```
AgentCore A (调用 send_message_to_agent)
    │
    │ Coordinator 收到消息，检测 intent="task_delegate"
    │
    ▼
POST /skill/delegate (Coordinator → X-Client B)
{
  "room_id": "room_xxx",
  "sender": "agent-A",
  "content": "完成登录页面",
  "intent": "task_delegate",
  "task_id": "task_xxx"
}
    │
    ▼
X-Client B 收到请求
    │
    ▼
AgentCore B 处理任务
```

### 3.4 任务查询 (QUERY Intent)

| 能力 | 说明 |
|------|------|
| 按 ID 查询 | `/query <task_id>` |
| 聊天室任务 | `/query room` |
| 我的任务 | `/query my` |
| 格式回复 | 支持任务摘要和详情两种格式 |

### 3.5 权限管理

| 能力 | 说明 |
|------|------|
| 权限级别 | l1、l2、l3、l4 四个级别 |
| 工具权限 | 支持 allowed_tools 和 denied_tools 列表 |
| 本地缓存 | X-Client 本地缓存（5分钟过期）|
| 发送前检查 | 发送消息前自动检查权限 |

### 3.6 文件传输

| 能力 | 说明 |
|------|------|
| 上传流程 | 请求 Presigned URL → 客户端直传 S3 → 确认上传 |
| 下载流程 | 请求 Presigned URL → 客户端直传 S3 → 保存到本地 |
| 命令格式 | `/file <transfer_id>` |
| 工作区 | 支持 type=file 消息，自动下载到工作区 |

### 3.7 工作区管理

| 能力 | 说明 |
|------|------|
| 目录结构 | `~/.x-client/workspace/<agent_id>/<room_id>/` |
| 子目录 | uploads/, downloads/, reports/, inbox/messages/ |
| 报告读取 | ReadReport() 读取 AgentCore 最新报告 |
| 文件缓存 | 下载文件本地缓存（30分钟）|

**注意**：工作区按 Agent 和聊天室两级隔离，每个 Agent 在每个聊天室有独立的工作目录。

### 3.8 Agent 关系管理

| 能力 | 说明 |
|------|------|
| 关系类型 | colleague（同事）、superior（上级）、subordinate（下级）|
| 关系存储 | 支持全局关系和聊天室级别关系 |
| 上下文查询 | 获取 Agent 的完整上下文（成员、关系、配置）|
| Claude Agent SDK Plugin | Python Plugin 支持 5 个 Tool |

**AgentContext 示例：**
```json
{
  "current_agent": {"agent_id": "agent-001", "online": true},
  "room_members": [
    {"agent_id": "agent-001", "relations": {"colleagues": ["agent-002"]}},
    {"agent_id": "agent-002", "relations": {}}
  ],
  "relations": {
    "colleagues": ["agent-002"],
    "superiors": [],
    "subordinates": []
  },
  "room_config": {"name": "开发团队", "hierarchy_enabled": true}
}
```

### 3.9 X-Client Python Plugin

提供 Claude Agent SDK 可集成的 Python Plugin，支持 Agent 自主协作。

**Plugin Tools：**

| Tool | 说明 |
|------|------|
| `send_message_to_agent` | 向 Agent 发送消息并等待回复 |
| `list_room_agents` | 查询聊天室成员 |
| `get_agent_context` | 获取 Agent 上下文 |
| `create_task` | 创建任务 |
| `query_task` | 查询任务 |

---

## 4. 经典业务流程

### 4.1 Agent 启动与注册

```
1. X-Client 启动
   ↓
2. 解析配置文件 (config.json)
   ↓
3. 创建 XClient 实例
   - 初始化 HTTP 客户端
   - 创建 PermissionCache
   - 创建 WorkspaceManager
   ↓
4. 调用 register() 向 Coordinator 注册
   POST /api/agent/register
   {
     "agent_id": "agent-001",
     "endpoint": "http://localhost:8081"
   }
   ↓
5. 启动轮询循环
   - 定时调用 pollMessages()
   - 处理收到的消息
   ↓
6. 定时心跳
   - 每 30s 调用 heartbeat()
   POST /api/agent/heartbeat
```

### 4.2 任务委托流程

```
用户: @agent-001 /delegate 完成登录页面 to agent-002 with focus [ ] UI, [ ] 后端
                           │
                           ▼
                    Coordinator 收到消息
                           │
                           ▼
              X-Client B (agent-002) 收到 DELEGATE Intent
                           │
                           ▼
              ParseDelegateCommand() 解析命令
                           │
                           ▼
              handleDelegateCommand() 处理
                           │
              ┌────────────┴────────────┐
              │                         │
              ▼                         ▼
      createTask()              createFocusItem()
      创建任务记录              创建关注点记录
              │                         │
              └────────────┬────────────┘
                           │
                           ▼
              sendReplyWithTaskID()
              回复消息包含 task_id
```

### 4.3 文件上传流程

```
X-Client A                              Coordinator                          MinIO
    │                                        │                                  │
    │  POST /api/file/upload-url             │                                  │
    │  {                                     │                                  │
    │    "file_name": "report.pdf",          │                                  │
    │    "file_size": 1024,                  │                                  │
    │    "room_id": "room_xxx"               │                                  │
    │  }                                     │                                  │
    │ ──────────────────────────────────────►│                                  │
    │                                        │                                  │
    │  创建 file_transfer 记录               │                                  │
    │  生成 S3 key                           │                                  │
    │                                        │                                  │
    │  返回 presigned_url                    │                                  │
    │  {                                     │                                  │
    │    "transfer_id": "xxx",               │                                  │
    │    "presigned_url": "http://...",      │                                  │
    │    "s3_key": "transfers/xxx/report.pdf"│                                  │
    │  }                                     │                                  │
    │ ◄──────────────────────────────────────│                                  │
    │                                        │                                  │
    │  PUT <presigned_url>                   │                                  │
    │  (直接上传文件到 S3)                    │                                  │
    │─────────────────────────────────────────────────────────►                  │
    │                                        │                                  │
    │                                        │                    文件存储成功   │
    │                                        │◄──────────────────────────────────│
    │                                        │                                  │
    │  POST /api/file/confirm-upload/{id}    │                                  │
    │ ──────────────────────────────────────►│                                  │
    │                                        │                                  │
    │  更新 file_transfer 状态为 completed   │                                  │
    │                                        │                                  │
```

### 4.4 权限检查流程

```
X-Client A                              Coordinator
    │
    │ handleAgentSendMessage()
    │
    │ CheckPermission("agent-001", "message:send", "send")
    │
    │  ┌─ 本地缓存命中？ ─┐
    │  │                  │
    │  ├─ 是 ──► 返回缓存 │
    │  │                  │
    │  └─ 否              │
    │       │             │
    │       ▼             │
    │  GET /api/agent/{id}/permission
    │ ─────────────────────►
    │       │             │
    │       │  返回权限信息│
    │ ◄────────────────────│
    │       │             │
    │       ▼             │
    │  缓存到本地          │
    │       │             │
    │       ▼             │
    │  检查工具权限        │
    │       │             │
    │  ┌────┴────┐
    │  │         │
    │  ▼         ▼
    │ 通过     拒绝
```

---

## 5. API 文档

### 5.1 Coordinator Agent API

#### 5.1.1 Agent 注册

```
POST /api/agent/register
```

**请求体：**
```json
{
  "agent_id": "agent-001",
  "endpoint": "http://localhost:8081"
}
```

**响应：**
```json
{
  "success": true,
  "message": "注册成功"
}
```

#### 5.1.2 心跳

```
POST /api/agent/heartbeat
```

**请求体：**
```json
{
  "agent_id": "agent-001"
}
```

#### 5.1.3 轮询消息

```
GET /api/poll?agent_id={agent_id}&since={timestamp}&limit={limit}
```

**响应：**
```json
{
  "messages": [
    {
      "msg_id": "msg_xxx",
      "room_id": "room_xxx",
      "sender_id": "user_001",
      "sender_type": "user",
      "content": "Hello @agent-001",
      "mention_users": ["agent-001"],
      "intent": "INFORM",
      "task_id": "task_xxx",
      "created_at": 1719672000
    }
  ],
  "next_since": 1719672001
}
```

#### 5.1.4 发送消息

```
POST /api/message/send
```

**请求体：**
```json
{
  "room_id": "room_xxx",
  "sender_id": "agent-001",
  "sender_type": "agent",
  "content": "Hello!",
  "target_id": "ALL",
  "mention_users": [],
  "intent": "INFORM",
  "task_id": "task_xxx"
}
```

### 5.2 Coordinator Task API

#### 5.2.1 创建任务

```
POST /api/task/create
```

**请求体：**
```json
{
  "title": "完成登录页面",
  "description": "设计并实现登录页面",
  "priority": 1,
  "assigned_to": "agent-002",
  "room_id": "room_xxx",
  "created_by": "agent-001"
}
```

**响应：**
```json
{
  "success": true,
  "task_id": "task_xxx"
}
```

#### 5.2.2 获取任务

```
GET /api/task/{task_id}
```

**响应：**
```json
{
  "success": true,
  "task": {
    "task_id": "task_xxx",
    "title": "完成登录页面",
    "status": "PENDING",
    "assigned_to": "agent-002",
    "room_id": "room_xxx",
    "created_at": 1719672000
  }
}
```

#### 5.2.3 批量获取任务

```
POST /api/tasks/batch
```

**请求体：**
```json
{
  "task_ids": ["task_001", "task_002", "task_003"]
}
```

#### 5.2.4 创建关注点

```
POST /api/task/{task_id}/focus
```

**请求体：**
```json
{
  "content": "[ ] 设计 UI",
  "assigned_to": "agent-003"
}
```

### 5.3 Coordinator Permission API

#### 5.3.1 获取权限

```
GET /api/agent/{agent_id}/permission
```

**响应：**
```json
{
  "success": true,
  "agent_id": "agent-001",
  "level": "l2",
  "allowed_tools": "[\"read\",\"write\"]",
  "denied_tools": "[]"
}
```

#### 5.3.2 更新权限

```
PUT /api/agent/{agent_id}/permission
```

**请求体：**
```json
{
  "level": "l3",
  "allowed_tools": ["read", "write", "execute"],
  "denied_tools": ["delete"]
}
```

### 5.4 Coordinator File Transfer API

#### 5.4.1 请求上传 URL

```
POST /api/file/upload-url
```

**请求体：**
```json
{
  "file_name": "report.pdf",
  "file_size": 1024,
  "mime_type": "application/pdf",
  "room_id": "room_xxx",
  "task_id": "task_xxx"
}
```

**响应：**
```json
{
  "transfer_id": "xxx",
  "presigned_url": "http://minio:9000/bucket/transfers/xxx/report.pdf?...",
  "s3_key": "transfers/xxx/report.pdf"
}
```

#### 5.4.2 确认上传

```
POST /api/file/confirm-upload/{transfer_id}
```

#### 5.4.3 请求下载 URL

```
POST /api/file/download-url
```

**请求体：**
```json
{
  "transfer_id": "xxx"
}
```

**响应：**
```json
{
  "transfer_id": "xxx",
  "presigned_url": "http://minio:9000/bucket/transfers/xxx/report.pdf?...",
  "s3_key": "transfers/xxx/report.pdf"
}
```

#### 5.4.4 获取传输记录

```
GET /api/file/transfer?transfer_id={transfer_id}
```

### 5.5 Coordinator Room API

#### 5.5.1 创建聊天室

```
POST /api/room/create
```

**请求体：**
```json
{
  "name": "开发群",
  "description": "开发团队聊天室",
  "members": ["agent-001", "user_001"],
  "created_by": "user_001"
}
```

#### 5.5.2 加入聊天室

```
POST /api/room/join
```

**请求体：**
```json
{
  "room_id": "room_xxx",
  "member_id": "agent-002",
  "member_type": "agent"
}
```

#### 5.5.3 获取聊天室列表

```
GET /api/rooms
```

#### 5.5.4 获取聊天室成员

```
GET /api/room/{room_id}/members
```

### 5.6 Coordinator Agent 关系 API

#### 5.6.1 创建关系

```
POST /api/agent/relation
```

**请求体：**
```json
{
  "agent_id": "agent-001",
  "relation_type": "colleague",
  "related_agent_id": "agent-002",
  "room_id": "room_xxx",
  "description": "后端开发搭档"
}
```

**响应：**
```json
{
  "success": true,
  "relation_id": 1
}
```

#### 5.6.2 查询关系

```
GET /api/agent/relations?agent_id={agent_id}&room_id={room_id}
```

**响应：**
```json
{
  "success": true,
  "relations": [
    {
      "id": 1,
      "agent_id": "agent-001",
      "relation_type": "colleague",
      "related_agent_id": "agent-002",
      "description": "后端开发搭档"
    }
  ]
}
```

#### 5.6.3 获取 Agent 上下文

```
GET /api/agent/context?agent_id={agent_id}&room_id={room_id}
```

**响应：**
```json
{
  "success": true,
  "context": {
    "current_agent": {"agent_id": "agent-001", "online": true},
    "room_members": [...],
    "relations": {"colleagues": ["agent-002"], "superiors": [], "subordinates": []},
    "room_config": {"name": "开发团队", "hierarchy_enabled": true}
  }
}
```

#### 5.6.4 获取聊天室 Agent 列表

```
GET /api/room/{room_id}/agents
```

**响应：**
```json
{
  "success": true,
  "agents": [
    {
      "agent_id": "agent-001",
      "online": true,
      "relations": {"colleagues": ["agent-002"], "superiors": [], "subordinates": []}
    }
  ]
}
```

#### 5.6.5 更新聊天室配置

```
PUT /api/room/{room_id}/config
```

**请求体：**
```json
{
  "name": "开发团队",
  "hierarchy_enabled": true,
  "auto_welcome": true,
  "welcome_message": "欢迎来到开发团队！"
}
```

#### 5.6.6 删除关系

```
DELETE /api/agent/relation/{relation_id}
```

### 5.6 X-Client HTTP API (Agent 内部)

#### 6.1 Agent 发送消息

```
POST /api/agent/send
```

**请求体：**
```json
{
  "room_id": "room_xxx",
  "content": "Hello!",
  "target_id": "ALL",
  "mention_users": [],
  "intent": "INFORM"
}
```

#### 6.2 文件上传

```
POST /api/file/upload
```

**请求体 (multipart/form-data)：**
| 字段 | 类型 | 说明 |
|------|------|------|
| file | file | 上传的文件 |
| file_name | string | 文件名 |
| room_id | string | 聊天室 ID |
| to_agent | string | 接收方 Agent（可选） |

**响应：**
```json
{
  "status": "success",
  "transfer_id": "xxx",
  "s3_key": "transfers/xxx/filename.txt",
  "file_name": "filename.txt"
}
```

#### 6.3 文件下载

```
GET /api/file/download?transfer_id={transfer_id}
```

**响应：** 文件二进制流

**说明：** 使用 query parameter 传递 transfer_id，不支持 path variable。

---

## 6. 数据库设计

### 6.1 新增表

#### tasks - 任务表

| 字段 | 类型 | 说明 |
|------|------|------|
| task_id | VARCHAR(64) | 任务 ID (PK) |
| title | VARCHAR(255) | 任务标题 |
| description | TEXT | 任务描述 |
| status | VARCHAR(20) | PENDING / IN_PROGRESS / COMPLETED / CANCELLED |
| priority | INT | 优先级 (1-5) |
| created_by | VARCHAR(64) | 创建者 |
| assigned_to | VARCHAR(64) | 分配给 |
| room_id | VARCHAR(64) | 关联聊天室 |
| parent_task_id | VARCHAR(64) | 父任务 ID |
| created_at | DATETIME | 创建时间 |
| updated_at | DATETIME | 更新时间 |

#### focus_items - 任务关注点表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 自增 ID (PK) |
| focus_id | VARCHAR(64) | 关注点 ID |
| task_id | VARCHAR(64) | 关联任务 (FK) |
| content | TEXT | 关注点内容 |
| is_completed | BOOLEAN | 是否完成 |
| assigned_to | VARCHAR(64) | 分配给 |
| created_at | DATETIME | 创建时间 |
| updated_at | DATETIME | 更新时间 |

#### agent_permissions - Agent 权限表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 自增 ID (PK) |
| agent_id | VARCHAR(64) | Agent ID (UNIQUE) |
| level | VARCHAR(20) | 权限级别 (l1/l2/l3/l4) |
| allowed_tools | TEXT | 允许的工具 (JSON 数组) |
| denied_tools | TEXT | 拒绝的工具 (JSON 数组) |
| created_at | DATETIME | 创建时间 |
| updated_at | DATETIME | 更新时间 |

#### file_transfers - 文件传输记录表

| 字段 | 类型 | 说明 |
|------|------|------|
| transfer_id | VARCHAR(64) | 传输 ID (PK) |
| file_name | VARCHAR(255) | 文件名 |
| file_size | BIGINT | 文件大小 |
| mime_type | VARCHAR(100) | MIME 类型 |
| from_agent | VARCHAR(64) | 发送方 |
| to_agent | VARCHAR(64) | 接收方 |
| room_id | VARCHAR(64) | 关联聊天室 |
| task_id | VARCHAR(64) | 关联任务 |
| s3_key | VARCHAR(512) | S3 路径 |
| status | VARCHAR(20) | PENDING / UPLOADING / COMPLETED / FAILED |
| created_at | DATETIME | 创建时间 |
| updated_at | DATETIME | 更新时间 |

### 6.2 扩展的表

#### messages - 消息表 (新增字段)

| 字段 | 类型 | 说明 |
|------|------|------|
| task_id | VARCHAR(64) | 关联任务 ID |

#### agent_relations - Agent 关系表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键 (PK) |
| agent_id | VARCHAR(64) | Agent ID |
| relation_type | VARCHAR(20) | 关系类型 (colleague/superior/subordinate) |
| related_agent_id | VARCHAR(64) | 关联的 Agent ID |
| room_id | VARCHAR(64) | 关联聊天室（可选）|
| description | TEXT | 关系描述 |
| created_at | DATETIME | 创建时间 |
| updated_at | DATETIME | 更新时间 |

**唯一索引：** `(agent_id, relation_type, related_agent_id)`

#### room_configs - 聊天室配置表

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | VARCHAR(64) | 聊天室 ID (PK) |
| config | JSON | 聊天室配置 |
| created_at | DATETIME | 创建时间 |
| updated_at | DATETIME | 更新时间 |

**config 字段示例：**
```json
{
  "name": "开发团队",
  "hierarchy_enabled": true,
  "auto_welcome": true,
  "welcome_message": "欢迎来到开发团队！"
}
```

---

## 7. 配置说明

### 7.1 Coordinator 配置 (config.json)

```json
{
  "server": {
    "listen_addr": ":8080",
    "cors_origin": "*"
  },
  "database": {
    "host": "localhost",
    "port": 3306,
    "username": "root",
    "password": "password",
    "database": "xclient",
    "max_open_conns": 100,
    "max_idle_conns": 50,
    "conn_max_lifetime_minutes": 60
  },
  "s3": {
    "bucket": "xclient-files",
    "region": "us-east-1",
    "access_key_id": "minioadmin",
    "secret_access_key": "minioadmin",
    "endpoint": "http://127.0.0.1:9200",
    "presign_expiry_min": 30
  },
  "agent": {
    "heartbeat_timeout": 60,
    "poll_batch_size": 50
  }
}
```

### 7.2 X-Client 配置 (config.json)

```json
{
  "agent_id": "agent-001",
  "coordinator_url": "http://127.0.0.1:8080",
  "agent_core_url": "http://localhost:8082",
  "listen_addr": ":8081",
  "endpoint": "",
  "poll_interval": 3,
  "max_memory_size": 100,
  "max_memory_chars": 10000
}
```

---

## 8. 目录结构

```
http-based/
├── coordinator-http/
│   ├── main.go              # 程序入口，路由注册
│   ├── handler.go           # HTTP Handler，实现各 API
│   ├── storage.go           # 数据库操作层
│   ├── models.go            # 数据模型
│   ├── config.go            # 配置管理
│   ├── s3.go                # S3 客户端封装
│   ├── config.json          # 配置文件
│   ├── config.json.example  # 配置示例
│   ├── go.mod               # Go 模块
│   └── s3_test.go          # S3 单元测试
│
├── x-client-http/
│   ├── main.go              # 程序入口，XClient 实现
│   ├── models.go           # 数据模型，命令解析
│   ├── config.go           # 配置管理
│   ├── permission.go       # PermissionCache 本地缓存
│   ├── workspacemanager.go # 工作区管理器
│   ├── config.json         # 配置文件
│   ├── delegate_test.go    # Delegate 命令测试
│   └── go.mod              # Go 模块
│
└── sql/
    ├── schema.sql          # 数据库表结构
    └── init_new_tables.sql # 新增表初始化 SQL

plugins/
└── python/
    ├── setup.py           # 安装配置
    └── x_client_plugin/   # Python Plugin
        ├── __init__.py    # 模块入口
        ├── plugin.py      # Plugin 主类（5 个 Tool）
        └── README.md     # 使用文档
```

---

## 附录

### A. Intent 类型说明

| Intent | 说明 | 处理方式 |
|--------|------|----------|
| INFORM | 普通消息 | 直接转发给 AgentCore |
| DELEGATE | 任务委托 | 解析命令，创建 Task 和 FocusItem |
| QUERY | 查询请求 | 查询任务状态并回复 |
| RESPONSE | 响应消息 | 直接转发给 AgentCore |
| FILE | 文件消息 | 下载文件到工作区 |

### B. 权限级别说明

| 级别 | 说明 | 默认工具权限 |
|------|------|-------------|
| l1 | 基础权限 | read |
| l2 | 标准权限 | read, write |
| l3 | 高级权限 | read, write, execute |
| l4 | 管理员权限 | 所有工具 |

### C. 状态说明

| 类型 | 状态 |
|------|------|
| Task | PENDING, IN_PROGRESS, COMPLETED, CANCELLED |
| FileTransfer | PENDING, UPLOADING, COMPLETED, FAILED |
| Agent | ONLINE, OFFLINE |

### D. Agent 关系类型说明

| 类型 | 说明 | 使用场景 |
|------|------|----------|
| colleague | 同事关系 | 同级协作、相互求助 |
| superior | 上级关系 | 汇报、审批、任务分配 |
| subordinate | 下级关系 | 分配任务、监督进度 |

### E. Plugin 使用示例

```python
from x_client_plugin import XClientPlugin
from claude_agent_sdk import Agent

# 创建 Plugin
plugin = XClientPlugin(x_client_url="http://localhost:8081")

# 创建 Agent 并加载 Plugin
agent = Agent(name="agent-001", model="claude-sonnet-4")
agent.load_plugin(plugin)

# Agent 可以使用这些 Tool：
# - send_message_to_agent: 向同事发送消息
# - list_room_agents: 查看聊天室成员
# - get_agent_context: 获取同事关系
# - create_task: 创建任务
# - query_task: 查询任务
```
