# 多智能体沙箱群聊系统 - 项目概览

## 1. 项目简介

这是一个支持多智能体（Agent）在群聊环境中进行自主协作的沙箱系统。用户可以在聊天室内与多个 AI Agent 进行对话，Agent 之间可以通过 @ 互相唤醒进行协作。

### 1.1 核心特性

- **群聊管理**：创建、加入、删除聊天室
- **@ 唤醒**：通过 @ 提及唤醒特定 Agent
- **发言控制**：基于令牌锁的发言冲突解决
- **历史同步**：新加入的成员自动同步最近消息
- **上下文感知**：Agent 拥有多轮对话记忆
- **自动重连**：网络波动后自动恢复连接
- **持久化存储**：消息历史持久化到数据库

---

## 2. 快速开始

### 2.1 前置要求

- Go 1.21+
- Git

### 2.2 编译项目

```bash
# 克隆项目
git clone <repository-url>
cd x-client

# 编译所有组件
cd coordinator && go build -o bin/coordinator . && cd ..
cd agentcore-mock && go build -o bin/agentcore-mock . && cd ..
cd x-client && go build -o bin/x-client . && cd ..
cd ui-test/manager && go build -o bin/manager . && cd ../..
```

### 2.3 启动服务

```bash
# 方式一：使用脚本一键启动
./start.sh

# 方式二：手动启动
# 终端 1: 启动 Manager (Web UI)
./ui-test/manager/bin/manager

# 终端 2-4: 启动协调器和 Agent
./coordinator/bin/coordinator
./agentcore-mock/bin/agentcore-mock
./x-client/bin/x-client --agent-id=agent_1 --coordinator=ws://localhost:8080/ws/chat --agentcore=http://localhost:8000 --listen=:8001
```

### 2.4 访问 Web UI

打开浏览器访问：http://localhost:9000

---

## 3. 组件说明

| 组件 | 说明 | 默认端口 |
|------|------|----------|
| **Manager** | Web UI 和服务管理 | :9000 |
| **Coordinator** | 消息中枢 | :8080 |
| **x-client** | Agent 客户端 | :8001+ |
| **AgentCore** | AI 响应服务 (Mock) | :8000+ |

---

## 4. 使用流程

### 4.1 创建聊天室

1. 在 Web UI 点击"启动协调器"
2. 点击"启动 Agent"（可以启动多个）
3. 在"创建聊天室"输入聊天室名称
4. 点击"创建"

### 4.2 发送消息

1. 选择聊天室
2. 选择发送者身份（用户或某个 Agent）
3. 在输入框输入消息
4. 点击发送

### 4.3 @ 唤醒 Agent

1. 输入 `@agent_1 你好` 格式的消息
2. 消息会发送给 agent_1
3. agent_1 被唤醒后会调用 AgentCore 生成响应
4. 响应会自动发送到聊天室

---

## 5. API 文档

### 5.1 Coordinator API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| GET | `/api/rooms` | 获取聊天室列表 |
| POST | `/api/room/create` | 创建聊天室 |
| DELETE | `/api/room/delete` | 删除聊天室 |
| GET | `/metrics` | 获取指标 |
| WS | `/ws/chat` | WebSocket 连接 |

详细文档：[coordinator.md](coordinator.md)

### 5.2 x-client API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| POST | `/skill/send` | Skill 回调发送消息 |

详细文档：[x-client.md](x-client.md)

---

## 6. 配置文件

### 6.1 Coordinator 配置

创建 `coordinator/config.json`：

```json
{
  "listen_addr": ":8080",
  "storage_type": "sqlite",
  "storage_path": "data/coordinator.db",
  "max_history": 50
}
```

### 6.2 x-client 配置

创建 `x-client/config.json`：

```json
{
  "agent_id": "agent_1",
  "coordinator": "ws://localhost:8080/ws/chat",
  "agentcore": "http://localhost:8000",
  "listen": ":8001"
}
```

### 6.3 Manager 配置

创建 `ui-test/manager/config.json`（可选）：

```json
{
  "coordinator_port": 8080,
  "manager_port": 9000,
  "coordinator_dir": "../coordinator",
  "xclient_dir": "../x-client",
  "agentcore_dir": "../agentcore-mock"
}
```

---

## 7. 数据库

系统使用 SQLite 作为默认数据库，文件位于 `coordinator/data/coordinator.db`。

### 7.1 表结构

- **rooms**: 聊天室信息
- **members**: 聊天室成员
- **messages**: 聊天消息

---

## 8. 扩展开发

### 8.1 添加新的 AI 提供商

1. 实现 `AgentCore` 接口：

```go
type AgentCore interface {
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}
```

2. 替换 `agentcore-mock` 为新的实现

### 8.2 添加 Skill 支持

1. 在 x-client 的 `/skill/send` 端点添加处理逻辑
2. 返回结果通过 x-client 发送到聊天室

### 8.3 切换数据库

修改 `coordinator/config.json`：

```json
{
  "storage_type": "mysql",
  "mysql_host": "localhost",
  "mysql_port": 3306,
  "mysql_user": "root",
  "mysql_password": "password",
  "mysql_database": "coordinator"
}
```

---

## 9. 文档目录

- [README.md](README.md) - 项目概览
- [architecture.md](architecture.md) - 系统架构文档
- [coordinator.md](coordinator.md) - Coordinator 实现文档
- [x-client.md](x-client.md) - x-client 实现文档

---

## 10. 常见问题

### Q: 启动后 WebSocket 连接失败？

A: 确保 Coordinator 已启动并监听 :8080 端口

### Q: Agent 不响应 @ 消息？

A: 检查 Agent 是否已加入聊天室，查看 x-client 日志

### Q: 如何查看日志？

A: 查看各组件的 stdout 输出，或配置日志文件输出

### Q: 如何添加更多 Agent？

A: 启动多个 AgentCore 和 x-client 实例，每个使用不同的端口
