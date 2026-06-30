# X-Client + Clawith A2A 改造进度

> 记录 x-client-http 的 Clawith A2A 集成改造进度

## 改造计划总览

| Phase | 内容 | 状态 |
|-------|------|------|
| Phase 1 | 数据库与基础设施 | ✅ 已完成 |
| Phase 2 | Task & Focus 系统 | ✅ 已完成 |
| Phase 3 | Permission 服务 | ✅ 已完成 |
| Phase 4 | Intent 路由增强 | ✅ 已完成 |
| Phase 5 | File Transfer + Workspace Manager | ✅ 已完成 |
| Phase 6 | 测试与优化 | ✅ 已完成 |

---

## Phase 1: 数据库与基础设施

### 目标
- [x] 新增 `tasks`、`focus_items`、`agent_permissions`、`file_transfers` 表
- [x] 实现 S3 客户端封装（coordinator-http）
- [x] 更新配置管理支持 S3

### 改动文件
- `sql/schema.sql` - 新增表 ✅
- `coordinator-http/models.go` - 新增模型 ✅
- `coordinator-http/storage.go` - 新增存储方法 ✅
- `coordinator-http/s3.go` - S3 客户端（新增）✅
- `coordinator-http/config.go` - S3 配置 ✅
- `coordinator-http/go.mod` - AWS SDK 依赖 ✅
- `coordinator-http/config.json.example` - S3 配置示例 ✅

### 详细进度

#### 1.1 新增数据库表
- [x] `tasks` 表 - 任务管理
- [x] `focus_items` 表 - 任务关注点
- [x] `agent_permissions` 表 - Agent 权限
- [x] `file_transfers` 表 - 文件传输记录
- [x] `messages` 表扩展 - 增加 `task_id` 字段

#### 1.2 S3 客户端
- [x] S3 客户端封装
- [x] Presigned URL 生成（上传播放、下载）
- [x] S3 配置管理

---

## Phase 2: Task & Focus 系统

### 目标
- [x] Coordinator Task CRUD API
- [x] Coordinator Focus Item API
- [x] X-Client 解析 `/delegate` 命令
- [x] 消息携带 `task_id`

### 改动文件
- `coordinator-http/handler.go` - Task/Focus API handlers ✅
- `coordinator-http/models.go` - Task/Focus 模型 + CreateTaskRequest.CreatedBy ✅
- `coordinator-http/main.go` - Task/Focus API 路由注册 ✅
- `x-client-http/models.go` - PollMessage/SendMessageRequest + TaskID 字段 ✅
- `x-client-http/models.go` - CreateTaskRequest, Task, DelegateCommand, ParseDelegateCommand ✅
- `x-client-http/main.go` - /delegate 命令解析、handleDelegateCommand、createTask、sendReplyWithTaskID ✅

---

## Phase 3: Permission 服务

### 目标
- [x] Coordinator Permission API
- [x] X-Client PermissionCache 本地缓存
- [x] `checkPermission()` 集成到消息发送流程

### 改动文件
- `coordinator-http/handler.go` - Permission handlers (Get/Upsert/Delete/Check) ✅
- `coordinator-http/main.go` - Permission API 路由注册 ✅
- `x-client-http/permission.go` - PermissionCache 本地缓存（新增）✅
- `x-client-http/main.go` - XClient 新增 permissionCache 字段 ✅
- `x-client-http/main.go` - handleAgentSendMessage 集成权限检查 ✅

---

## Phase 4: Intent 路由增强

### 目标
- [x] X-Client 扩展 PollMessage 增加 TaskID（Phase 2 已完成）
- [x] DELEGATE Intent 处理（创建任务）
- [x] QUERY Intent 处理（查询任务）
- [x] 消息与 Task 的绑定

### 改动文件
- `x-client-http/main.go` - Intent 路由 switch-case（DELEGATE/QUERY/RESPONSE/default）✅
- `x-client-http/main.go` - handleQueryCommand、getTask、getTasksByRoom、getTasksByAgent ✅
- `x-client-http/main.go` - formatTaskResponse、formatTaskSummary ✅

---

## Phase 5: File Transfer + Workspace Manager

### 目标
- [x] Coordinator Presigned URL API
- [x] X-Client WorkspaceManager 实现
- [x] 文件上传/下载实现
- [x] `handleFileMessage()` 保存到工作区
- [x] `ReadReport()` 读取 AgentCore 报告
- [x] Room 消息类型集成（type=file）

### 改动文件
- `coordinator-http/handler.go` - Handler 新增 s3Client 字段 ✅
- `coordinator-http/handler.go` - NewHandler 新增 s3Client 参数 ✅
- `coordinator-http/handler.go` - FileTransfer handlers ✅
- `coordinator-http/main.go` - 初始化 S3Client ✅
- `coordinator-http/main.go` - File Transfer API 路由注册 ✅
- `x-client-http/workspacemanager.go` - WorkspaceManager（新增）✅
- `x-client-http/main.go` - handleFileMessage 文件处理 ✅

---

## Phase 6: 测试与优化

### 目标
- [x] 编译验证（go build + go vet）
- [x] 单元测试
- [x] 端到端测试（MySQL + MinIO 实际环境）
- [ ] 性能优化

### 已完成
- [x] coordinator-http: `go build` + `go vet` 通过
- [x] x-client-http: `go build` + `go vet` 通过
- [x] x-client-http: ParseDelegateCommand 测试（7个用例全部通过）
- [x] coordinator-http: GenerateS3Key 测试（5个用例全部通过）
- [x] Permission API 端到端测试（PUT /api/agent/{id}/permission）
- [x] Task API 端到端测试（POST /api/task/create）
- [x] Focus Item API 端到端测试（POST /api/task/{id}/focus）
- [x] File Transfer API 端到端测试（POST /api/file/upload-url）
- [x] S3 文件实际上传验证（hello.txt 上传到 MinIO）

### 待完成
- 性能优化（可选增强）

### 已完成性能优化
- [x] 数据库连接池配置（100 max open, 50 idle, 1h lifetime）
- [x] PermissionCache 后台清理机制（StartCleanupRoutine）
- [x] 批量任务查询 API（POST /api/tasks/batch，最多 100 个任务）

---

## 变更日志

| 日期 | Phase | 变更内容 |
|------|-------|----------|
| 2026-06-29 | Phase 1 | 创建改造进度文档 |
| 2026-06-29 | Phase 1 | 新增 tasks, focus_items, agent_permissions, file_transfers 表 |
| 2026-06-29 | Phase 1 | 新增 messages.task_id 字段 |
| 2026-06-29 | Phase 1 | 新增 Task, FocusItem, AgentPermission, FileTransfer 模型 |
| 2026-06-29 | Phase 1 | 新增 Task/Focus/Permission/FileTransfer 存储方法 |
| 2026-06-29 | Phase 1 | 新增 S3 客户端封装（Presigned URL） |
| 2026-06-29 | Phase 1 | 新增 S3 配置支持 |
| 2026-06-29 | Phase 2 | 新增 Coordinator Task CRUD API（handler + routes） |
| 2026-06-29 | Phase 2 | 新增 Coordinator Focus Item API（handler + routes） |
| 2026-06-29 | Phase 2 | X-Client PollMessage/SendMessageRequest 新增 TaskID 字段 |
| 2026-06-29 | Phase 2 | X-Client 新增 /delegate 命令解析（ParseDelegateCommand） |
| 2026-06-29 | Phase 2 | X-Client 新增 handleDelegateCommand、createTask、createFocusItem、sendReplyWithTaskID |
| 2026-06-29 | Phase 3 | Coordinator 新增 Permission API（Get/Upsert/Delete/Check） |
| 2026-06-29 | Phase 3 | X-Client 新增 PermissionCache 本地缓存 |
| 2026-06-29 | Phase 3 | X-Client handleAgentSendMessage 集成权限检查 |
| 2026-06-29 | Phase 4 | X-Client Intent 路由增强（DELEGATE/QUERY/RESPONSE） |
| 2026-06-29 | Phase 4 | X-Client 新增 handleQueryCommand 及任务查询方法 |
| 2026-06-29 | Phase 5 | Coordinator Handler 新增 s3Client 字段及初始化 |
| 2026-06-29 | Phase 5 | Coordinator 新增 FileTransfer API（Presigned URL） |
| 2026-06-29 | Phase 5 | X-Client 新增 WorkspaceManager 工作区管理器 |
| 2026-06-29 | Phase 5 | X-Client 新增 handleFileMessage 文件处理 |
| 2026-06-29 | Phase 5 | X-Client 新增 ReadReport/ListReports 读取报告 |
| 2026-06-29 | Phase 5 | X-Client 新增 PollMessage.Type 字段支持 type=file |
| 2026-06-29 | Phase 6 | X-Client 新增 delegate_test.go 单元测试 |
| 2026-06-29 | Phase 6 | Coordinator 新增 s3_test.go 单元测试 |
| 2026-06-29 | Phase 5 | Coordinator S3 客户端 EnsureBucket 自动创建 bucket |
| 2026-06-29 | Phase 5 | Coordinator S3 所有 API 添加 context.Background() |
| 2026-06-29 | Phase 6 | 数据库初始化 SQL（tasks/focus_items/agent_permissions/file_transfers）|
| 2026-06-29 | Phase 6 | Coordinator S3 Presigned URL 端到端验证通过 |
| 2026-06-29 | Phase 6 | X-Client PermissionCache 新增 StartCleanupRoutine 后台清理 |
| 2026-06-29 | Phase 6 | Coordinator 新增批量任务查询 API（/api/tasks/batch）|
| 2026-06-29 | 新增 | X-Client HTTP 新增 /skill/delegate 和 /skill/send 接口支持 Agent 自主委托 |

---

# 新增功能计划：Agent 关系感知与协作系统

## 文档

| 文档 | 说明 |
|------|------|
| [agent-collaboration-design.md](./agent-collaboration-design.md) | 设计方案 |
| [agent-collaboration-implementation.md](./agent-collaboration-implementation.md) | 实施指南 |

## 实施阶段

| 阶段 | 内容 | 优先级 | 状态 |
|------|------|--------|------|
| Phase 0 | 基础设施准备 | P0 | ✅ 完成 |
| Phase 1 | 数据库设计（agent_relations, room_configs）| P0 | ✅ 完成 |
| Phase 2 | Coordinator API 扩展 | P0 | ✅ 完成 |
| Phase 3 | X-Client API 增强 | P1 | ✅ 完成 |
| Phase 4 | X-Client Plugin 开发 | P1 | ✅ 完成 |
| Phase 5 | 测试与集成 | P1 | ⏳ 待开始 |
| Phase 6 | 高级功能（欢迎消息等）| P2 | ⏳ 待开始 |

---

## Phase 1: 数据库设计 ✅

### 目标
- [x] 创建 `agent_relations` 表（Agent 关系）
- [x] 创建 `room_configs` 表（聊天室配置）
- [x] 扩展 `agents` 表（增加 role、description 字段）

### 改动文件
- [x] `sql/agent_relations.sql` - 新增 SQL 文件

### 数据库表结构

#### agent_relations 表
| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键 |
| agent_id | VARCHAR(64) | Agent ID |
| relation_type | VARCHAR(20) | colleague/superior/subordinate |
| related_agent_id | VARCHAR(64) | 关联的 Agent ID |
| room_id | VARCHAR(64) | 关联的聊天室（可选）|
| description | TEXT | 关系描述 |

#### room_configs 表
| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | VARCHAR(64) | 聊天室 ID (PK) |
| config | JSON | 聊天室配置 |

---

## Phase 2: Coordinator API 扩展 ✅

### 目标
- [x] 新增 Agent 关系管理 API
- [x] 新增 Agent 上下文查询 API
- [x] 新增聊天室配置 API

### 改动文件

#### models.go
- [x] 新增 `RelationType` 常量
- [x] 新增 `AgentRelation` 结构体
- [x] 新增 `RoomConfig` 结构体
- [x] 新增 `Relations` 结构体
- [x] 新增 `AgentContext` 结构体
- [x] 新增 `AgentInfo` 结构体
- [x] 新增 `CreateRelationRequest` 结构体
- [x] 新增 `UpsertRoomConfigRequest` 结构体

#### storage.go
- [x] 新增 `CreateRelation()`
- [x] 新增 `GetAgentRelations()`
- [x] 新增 `DeleteRelation()`
- [x] 新增 `GetRelationsSummary()`
- [x] 新增 `GetRoomConfig()`
- [x] 新增 `UpsertRoomConfig()`
- [x] 新增 `GetAgentInfo()`
- [x] 新增 `GetRoomAgents()`

#### handler.go
- [x] 新增 `CreateRelationHandler` - POST /api/agent/relation
- [x] 新增 `GetAgentRelationsHandler` - GET /api/agent/relations
- [x] 新增 `DeleteRelationHandler` - DELETE /api/agent/relation/{id}
- [x] 新增 `GetAgentContextHandler` - GET /api/agent/context
- [x] 新增 `GetRoomAgentsHandler` - GET /api/room/{room_id}/agents
- [x] 新增 `UpdateRoomConfigHandler` - PUT /api/room/{room_id}/config

#### main.go
- [x] 新增 6 个路由注册

### 新增 API 端点

| 方法 | 端点 | 说明 |
|------|------|------|
| POST | `/api/agent/relation` | 创建关系 |
| GET | `/api/agent/relations` | 获取 Agent 关系 |
| DELETE | `/api/agent/relation/{id}` | 删除关系 |
| GET | `/api/agent/context` | 获取 Agent 上下文 |
| GET | `/api/room/{room_id}/agents` | 获取聊天室 Agent |
| PUT | `/api/room/{room_id}/config` | 更新聊天室配置 |

---

## Phase 3: X-Client API 增强 ✅

### 目标
- [x] X-Client 支持获取 Agent 上下文
- [x] X-Client 支持构建协作提示

### 改动文件

#### models.go
- [x] 新增 7 个结构体（AgentRelation, Relations, AgentInfo, RoomConfig, AgentContext, CreateRelationRequest, UpsertRoomConfigRequest）

#### main.go
- [x] 新增 `GetAgentContext()` - 获取 Agent 上下文
- [x] 新增 `GetRoomAgents()` - 获取聊天室成员
- [x] 新增 `GetAgentRelations()` - 获取 Agent 关系
- [x] 新增 `BuildCollaborationPrompt()` - 构建协作提示

---

## Phase 4: X-Client Plugin 开发 ✅

### 目标
- [x] 开发 Python Plugin 供 Claude Agent SDK 使用
- [x] 实现 5 个 Tool

### 改动文件

- [x] `plugins/python/x_client_plugin/__init__.py` - 模块入口
- [x] `plugins/python/x_client_plugin/plugin.py` - Plugin 主类（约 400 行）
- [x] `plugins/python/x_client_plugin/README.md` - 使用文档
- [x] `plugins/python/setup.py` - 安装配置

### Plugin Tools

| Tool | 说明 |
|------|------|
| `send_message_to_agent` | 向 Agent 发送消息 |
| `list_room_agents` | 获取聊天室成员 |
| `get_agent_context` | 获取 Agent 上下文 |
| `create_task` | 创建任务 |
| `query_task` | 查询任务 |

---

## Phase 5: 测试与集成 ✅

### 已测试内容
- [x] 创建 Agent 关系 - API 测试通过
- [x] 查询 Agent 上下文 - API 测试通过
- [x] 创建聊天室 - API 测试通过
- [x] 设置聊天室配置 - API 测试通过

### 测试结果

```json
// Agent 上下文响应
{
  "context": {
    "current_agent": {"agent_id": "agent-001", "online": true},
    "room_members": [
      {"agent_id": "agent-001", "relations": {"colleagues": ["agent-002"]}},
      {"agent_id": "agent-002", "relations": {}}
    ],
    "relations": {"colleagues": ["agent-002"], "superiors": [], "subordinates": []},
    "room_config": {"name": "开发团队", "hierarchy_enabled": true}
  }
}
```

### 修复的问题
- [x] 日期格式问题 - 使用 MySQL NOW() 函数
- [x] nil 切片问题 - 初始化为空切片
- [x] 字段类型不匹配 - time.Time vs int64

## 核心功能

### 1. Agent 关系管理
- 同事关系 (colleague)
- 上下级关系 (superior/subordinate)

### 2. X-Client Plugin Tools
- `send_message_to_agent` - 向 Agent 发送消息
- `list_room_agents` - 获取聊天室成员
- `get_agent_context` - 获取 Agent 上下文
- `create_task` - 创建任务
- `query_task` - 查询任务
