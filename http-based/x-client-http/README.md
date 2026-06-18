# x-client HTTP 版本配置

## 配置说明

### 基本配置

| 配置项 | 说明 | 示例 |
|--------|------|------|
| `agent_id` | Agent 唯一标识 | `agent_1` |
| `coordinator_url` | Coordinator HTTP 地址 | `http://localhost:8080` |
| `agentcore_url` | AgentCore HTTP 地址 | `http://localhost:8000` |
| `listen_addr` | x-client HTTP 监听地址 | `:8001` |
| `endpoint` | x-client 暴露给 Coordinator 的地址 | `http://localhost:8001` |

### 轮询配置

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `poll_interval` | 轮询间隔（秒） | 5 |
| `poll_batch_size` | 每次轮询获取的最大消息数 | 50 |

### 上下文配置

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `max_memory_size` | 记忆窗口最大消息数 | 50 |
| `max_memory_chars` | 记忆窗口最大字符数 | 2000 |

## 环境变量

所有配置项都可以通过环境变量覆盖：

| 环境变量 | 对应配置 |
|----------|----------|
| `AGENT_ID` | agent_id |
| `COORDINATOR_URL` | coordinator_url |
| `AGENTCORE_URL` | agentcore_url |
| `LISTEN_ADDR` | listen_addr |

## 使用示例

### 1. 单 Agent 启动

```bash
cd x-client-http
go mod tidy
go run main.go
```

### 2. 多 Agent 启动

```bash
# Agent 1
AGENT_ID=agent_1 LISTEN_ADDR=:8001 go run main.go

# Agent 2
AGENT_ID=agent_2 LISTEN_ADDR=:8002 go run main.go
```

### 3. Docker 部署

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod tidy && go build -o x-client main.go

FROM alpine
COPY --from=builder /app/x-client /app/x-client
COPY config.json /app/config.json
WORKDIR /app
CMD ["/app/x-client"]
```
