# 修复多 Tab 登录问题 - 基于数据库的用户会话管理

## 问题分析

**当前问题**：
- Coordinator 在内存中使用 `userConns[userID]` 存储 WebSocket 连接
- 同一用户多 Tab 登录时，后面的会覆盖前面的连接
- 导致只有最后一个 Tab 能收到消息

**问题代码位置**：`handler.go` 第 641 行
```go
h.userConns[userID] = userConn  // 覆盖之前的连接
```

## 解决方案

创建 `user_room_sessions` 表存储用户与聊天室的会话关系，实现：
1. 用户加入聊天室时，在数据库中创建会话记录
2. 用户离开聊天室时，删除会话记录
3. WebSocket 连接时，从数据库加载用户已订阅的房间

## 实施步骤

### 1. 数据库层 - 添加 user_room_sessions 表和操作方法

**文件**：`storage.go`

**新增表结构**：
```sql
CREATE TABLE IF NOT EXISTS user_room_sessions (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    room_id VARCHAR(255) NOT NULL,
    connection_id VARCHAR(255) NOT NULL,
    connected_at DATETIME NOT NULL,
    last_active_at DATETIME NOT NULL,
    UNIQUE KEY uk_user_room (user_id, room_id),
    KEY idx_connection (connection_id),
    KEY idx_user (user_id),
    KEY idx_room (room_id)
);
```

**新增 Storage 方法**：
- `CreateUserRoomSession(userID, roomID, connectionID)` - 创建会话
- `DeleteUserRoomSession(userID, roomID)` - 删除会话
- `GetUserRoomSessions(userID)` - 获取用户所有会话的房间列表
- `CleanupStaleSessions(maxAge)` - 清理超时会话

### 2. Handler 层 - 修改连接管理和房间订阅逻辑

**文件**：`handler.go`

**修改内容**：

1. **修改 UserConn 结构**，添加 ConnectionID：
```go
type UserConn struct {
    UserID       string
    ConnectionID string  // 新增：唯一连接标识
    Conn         *websocket.Conn
    Send         chan []byte
    Rooms        map[string]bool
    CloseChan    chan struct{}
}
```

2. **修改 WebSocket 连接建立逻辑**：
   - 生成唯一的 ConnectionID
   - 从数据库加载用户已订阅的房间列表
   - 更新内存中的映射为 `userConns[connectionID]`

3. **修改 join 房间逻辑**：
   - join 时同时写入数据库 `user_room_sessions`
   - 更新内存 `userRooms[userID][roomID] = true`

4. **修改 leave 房间逻辑**：
   - 从数据库删除会话记录
   - 从内存 `userRooms[userID]` 删除

5. **修改通知推送逻辑**：
   - 改为遍历数据库查询在线用户，而非内存映射

### 3. 初始化 - 创建数据库表

**文件**：`storage.go` 的 `Init` 方法中添加：
```go
s.db.Exec(`CREATE TABLE IF NOT EXISTS user_room_sessions (...)`)
```

## 文件修改清单

| 文件 | 修改内容 |
|------|---------|
| `storage.go` | 添加表创建、Session CRUD 方法 |
| `handler.go` | 修改 UserConn 结构、连接管理、join/leave 逻辑 |
| `models.go` | 添加 Session 结构体（可选） |

## 验证步骤

1. 启动服务
2. 打开两个 Tab，登录同一用户，加入同一聊天室
3. 发送 `@agent_1` 消息
4. **预期**：两个 Tab 都能收到 agent 回复
5. 关闭一个 Tab，另一个 Tab 仍能正常接收消息
