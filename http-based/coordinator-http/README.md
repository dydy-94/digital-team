# Coordinator HTTP 版本配置

## 配置说明

### 基本配置

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `LISTEN_ADDR` | HTTP 监听地址 | `:8080` |

### 数据库配置

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `DB_HOST` | MySQL 主机 | `localhost` |
| `DB_PORT` | MySQL 端口 | `3306` |
| `DB_USER` | 数据库用户 | `root` |
| `DB_PASSWORD` | 数据库密码 | `` |
| `DB_NAME` | 数据库名 | `xclient` |

### 发言锁配置

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `SPEAKER_LOCK_TIMEOUT_MS` | 发言锁超时（毫秒） | `2000` |

### 心跳配置

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `HEARTBEAT_TIMEOUT_SEC` | Agent 心跳超时（秒） | `60` |

### 轮询配置

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `POLL_BATCH_SIZE` | 每次 poll 返回的最大消息数 | `50` |

### 消息保留

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `MESSAGE_RETENTION_DAYS` | 消息保留天数 | `7` |

## 启动示例

```bash
# 基本启动
LISTEN_ADDR=:8080 go run main.go

# 完整配置
LISTEN_ADDR=:8080 \
DB_HOST=localhost \
DB_PORT=3306 \
DB_USER=root \
DB_PASSWORD=secret \
DB_NAME=xclient \
go run main.go
```

## API 列表

### Agent API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/agent/register` | Agent 注册 |
| POST | `/api/agent/heartbeat` | Agent 心跳 |
| GET | `/api/poll` | Agent 轮询消息 |

### Room API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/rooms` | 获取聊天室列表 |
| POST | `/api/room/create` | 创建聊天室 |
| POST | `/api/room/join` | 加入聊天室 |
| GET | `/api/room/{room_id}/members` | 获取聊天室成员 |
| DELETE | `/api/room/{room_id}/leave/{member_id}` | 离开聊天室 |

### Message API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/message` | 发送消息 |

### WebSocket

| 方法 | 路径 | 说明 |
|------|------|------|
| WS | `/ws/user` | User WebSocket 连接 |

### 健康检查

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
