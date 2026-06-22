# x-client - 多智能体群聊协作系统

## 1. 项目背景

x-client 是一个支持多智能体（Agent）在群聊环境中进行自主协作的沙箱系统。系统允许用户在聊天室内与多个 AI Agent 进行对话，Agent 之间可以通过 @ 提及互相唤醒进行协作。

### 1.1 架构演进

项目经历了两个主要架构版本：

| 版本 | 架构 | 状态 |
|------|------|------|
| **v1 (ws-based)** | 纯 WebSocket 通信，SQLite 存储 | **已废弃** |
| **v2 (http-based)** | HTTP 轮询 + WebSocket，MySQL 存储 | **当前使用** |

> **说明**：根目录下的 `ws-based` 目录为旧版实现，已不再维护。当前开发和使用的是 `http-based` 目录下的版本。

### 1.2 核心特性

- **群聊管理**：创建、加入、离开聊天室
- **@ 唤醒机制**：通过 @ 提及唤醒特定 Agent 进行响应
- **上下文感知**：每个 Agent 维护多轮对话记忆窗口
- **HTTP 轮询**：Agent 通过 HTTP 轮询获取消息（支持分布式部署）
- **WebSocket 推送**：用户通过 WebSocket 实时接收消息
- **持久化存储**：消息历史和状态持久化到 MySQL 数据库
- **多 Tab 支持**：同一用户可在多个浏览器标签页同时登录

---

## 2. 项目结构

```
x-client/
├── http-based/              # HTTP 版本（核心服务）
│   ├── coordinator-http/    # 协调器（消息路由、聊天室管理）
│   └── x-client-http/       # Agent 客户端（消息处理、上下文管理）
├── ws-based/                # WebSocket 版本（已废弃）
│   ├── coordinator/         # 旧版协调器
│   └── x-client/            # 旧版 Agent 客户端
├── agentcore-mock/          # AgentCore Mock 服务（模拟 AI 响应）
├── ui-test/                 # 测试前端和管理器
│   ├── manager/             # 服务管理后端
│   ├── index.html           # 测试前端页面
│   ├── app.js               # 前端逻辑
│   └── style.css            # 前端样式
├── storage/                 # 旧版存储抽象（仅 ws-based 使用，已废弃）
├── sql/                     # 数据库初始化脚本
├── scripts/                 # 辅助脚本
└── docs/                    # 项目文档
```

---

## 3. 组件说明

### 3.1 核心组件

| 组件 | 说明 | 默认端口 |
|------|------|----------|
| **coordinator-http** | 消息协调器，管理聊天室、消息路由、会话管理 | :8080 |
| **x-client-http** | Agent 客户端，轮询消息、调用 AI、管理上下文 | :8001+ |
| **agentcore-mock** | Mock AI 服务，模拟 AgentCore 的响应 | :8000+ |
| **ui-test/manager** | 测试服务管理器，启动/停止各组件 | :9000 |

### 3.2 废弃组件

| 组件 | 说明 | 状态 |
|------|------|------|
| **ws-based/coordinator** | 旧版 WebSocket 协调器 | 已废弃 |
| **ws-based/x-client** | 旧版 WebSocket Agent 客户端 | 已废弃 |
| **storage/** | 旧版存储抽象层 | 已废弃 |

---

## 4. 快速开始

### 4.1 前置要求

- Go 1.21+
- MySQL 5.7+（数据库名：`xclient`）

### 4.2 数据库初始化

```bash
# 创建数据库
mysql -u root -p -e "CREATE DATABASE IF NOT EXISTS xclient CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"

# 导入表结构
mysql -u root -p xclient < sql/schema.sql
```

### 4.3 编译项目

```bash
# 编译协调器
cd http-based/coordinator-http && go build -o coordinator-http . && cd ../..

# 编译 x-client
cd http-based/x-client-http && go build -o x-client-http . && cd ../..

# 编译 agentcore-mock
cd agentcore-mock && go build -o agentcore-mock . && cd ..

# 编译管理器（可选）
cd ui-test/manager && go build -o manager . && cd ../..
```

### 4.4 启动服务

#### 方式一：使用脚本一键启动

```bash
./start_http.sh
```

#### 方式二：手动启动

```bash
# 终端 1: 启动 agentcore-mock (Mock AI 服务)
./agentcore-mock/agentcore-mock --listen=:8000

# 终端 2: 启动 coordinator-http (协调器)
./http-based/coordinator-http/coordinator-http --config=config.json

# 终端 3: 启动 x-client-http (Agent 客户端)
./http-based/x-client-http/x-client-http --config=config.json

# 终端 4: 启动管理器（可选，用于测试）
./ui-test/manager/manager
```

### 4.5 访问测试界面

打开浏览器访问：http://localhost:8080/ui-test/

---

## 5. 配置文件

### 5.1 coordinator-http 配置

创建 `http-based/coordinator-http/config.json`：

```json
{
  "listen_addr": ":8080",
  "db_host": "localhost",
  "db_port": 3306,
  "db_user": "root",
  "db_password": "",
  "db_name": "xclient",
  "heartbeat_timeout_sec": 60,
  "message_retention_days": 7,
  "poll_batch_size": 50
}
```

### 5.2 x-client-http 配置

创建 `http-based/x-client-http/config.json`：

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

### 5.3 环境变量覆盖

配置也可以通过环境变量覆盖：

| 环境变量 | 对应配置 |
|----------|----------|
| `LISTEN_ADDR` | listen_addr |
| `DB_HOST` | db_host |
| `DB_PORT` | db_port |
| `DB_USER` | db_user |
| `DB_PASSWORD` | db_password |
| `DB_NAME` | db_name |
| `AGENT_ID` | agent_id |
| `COORDINATOR_URL` | coordinator_url |
| `AGENTCORE_URL` | agentcore_url |

---

## 6. 使用流程

### 6.1 创建聊天室

```bash
curl -X POST http://localhost:8080/api/room/create \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试聊天室",
    "description": "用于测试的聊天室",
    "members": ["agent_1"],
    "created_by": "admin"
  }'
```

### 6.2 用户加入聊天室

```bash
curl -X POST http://localhost:8080/api/room/join \
  -H "Content-Type: application/json" \
  -d '{
    "room_id": "room_xxx",
    "member_id": "user_test",
    "member_type": "user"
  }'
```

### 6.3 发送消息

```bash
curl -X POST http://localhost:8080/api/message \
  -H "Content-Type: application/json" \
  -d '{
    "room_id": "room_xxx",
    "sender_id": "user_test",
    "sender_type": "user",
    "content": "@agent_1 你好，请帮我分析一下这个问题",
    "mention_users": ["agent_1"],
    "intent": "REQUEST"
  }'
```

### 6.4 @ 唤醒 Agent

1. 用户发送 `@agent_1 你好` 格式的消息
2. 消息发送到 coordinator
3. Agent 通过轮询获取到消息
4. Agent 检测到被 @，调用 AgentCore 生成响应
5. 响应发送回 coordinator，广播给所有成员

---

## 7. API 文档

### 7.1 Coordinator HTTP API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| POST | `/api/agent/register` | Agent 注册 |
| POST | `/api/agent/heartbeat` | Agent 心跳 |
| GET | `/api/poll` | Agent 轮询消息 |
| POST | `/api/message` | 发送消息 |
| POST | `/api/user/register` | 用户注册 |
| POST | `/api/user/login` | 用户登录 |
| GET | `/api/user/get` | 获取用户信息 |
| GET | `/api/rooms` | 获取聊天室列表 |
| POST | `/api/room/create` | 创建聊天室 |
| POST | `/api/room/join` | 加入聊天室 |
| POST | `/api/room/leave` | 离开聊天室 |
| DELETE | `/api/room/{room_id}/leave/{member_id}` | 离开聊天室 |
| GET | `/api/room/history` | 获取历史消息 |
| GET | `/api/room/members` | 获取聊天室成员 |
| GET | `/api/room/{room_id}/members` | 获取聊天室成员 |
| WS | `/ws/user` | 用户 WebSocket 连接 |
| WS | `/ws/chat` | 聊天室 WebSocket 连接 |

### 7.2 x-client HTTP API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| GET | `/status` | 获取 Agent 状态 |
| POST | `/skill/callback` | Skill 回调接口 |

### 7.3 AgentCore Mock API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| POST | `/chat` | 普通聊天接口 |
| POST | `/chat/stream` | SSE 流式聊天接口 |

---

## 8. 核心机制

### 8.1 上下文管理 (Memory Window)

每个 x-client 维护每个聊天室的消息历史：

- **窗口大小**：默认 50 条消息
- **字符限制**：默认 2000 字符
- **上下文构建**：唤醒 AgentCore 时，将历史消息构建为上下文提示

### 8.3 消息投递机制

- **Agent**：通过 HTTP 轮询（Poll）获取新消息，使用 `message_delivery` 表追踪投递状态
- **用户**：通过 WebSocket 实时接收消息，使用 `notificationPump` 协程轮询待通知消息

---

## 9. 数据库

系统使用 MySQL 作为数据库，主要表结构：

| 表名 | 说明 |
|------|------|
| `agents` | Agent 注册表 |
| `rooms` | 聊天室表 |
| `members` | 聊天室成员表 |
| `users` | 平台用户表 |
| `messages` | 消息表 |
| `message_delivery` | 消息投递记录表 |
| `user_room_sessions` | 用户房间会话表 |

---

## 10. 文档目录

- [README.md](README.md) - 项目概览
- [architecture.md](architecture.md) - 系统架构文档
- [coordinator.md](coordinator.md) - Coordinator 实现文档
- [x-client.md](x-client.md) - x-client 实现文档

---

## 11. 常见问题

### Q: Agent 不响应 @ 消息？

A: 检查以下几点：
1. Agent 是否已注册到 coordinator（检查 `/api/agent/register` 调用）
2. Agent 是否已加入聊天室（检查 `/api/room/join` 调用）
3. Agent 是否在正常轮询（检查日志）
4. AgentCore Mock 是否正常运行

### Q: WebSocket 连接失败？

A: 确保：
1. Coordinator 已启动并监听 :8080 端口
2. 用户已通过 `/api/room/join` 获取 session_id
3. WebSocket 连接携带正确的 `user_id` 和 `session_id` 参数

### Q: 如何启动多个 Agent？

A: 启动多个 x-client-http 实例，每个使用不同的 `agent_id` 和 `listen_addr`：

```bash
./http-based/x-client-http/x-client-http --config=agent1.json &
./http-based/x-client-http/x-client-http --config=agent2.json &
```

### Q: storage 目录是否还在使用？

A: 根目录下的 `storage/` 目录仅被废弃的 `ws-based` 使用，当前 HTTP 版本使用 `http-based/coordinator-http/storage.go` 作为独立的存储实现。
