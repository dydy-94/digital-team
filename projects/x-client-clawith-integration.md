# X-Client + Clawith A2A 结合方案

> 本文档描述如何将 X-Client HTTP 的消息传输能力与 Clawith A2A 的协作逻辑相结合，构建一个兼具轻量传输和企业级协作能力的多 Agent 系统。

## 目录

- [1. 背景与目标](#1-背景与目标)
- [2. 架构设计](#2-架构设计)
- [3. 工作区架构](#3-工作区架构)
- [4. 核心模块](#4-核心模块)
- [5. 数据库设计](#5-数据库设计)
- [6. API 设计](#6-api-设计)
- [7. 改动归属](#7-改动归属)
- [8. 实施计划](#8-实施计划)

---

## 1. 背景与目标

### 1.1 结合动机

| 需求 | X-Client HTTP | Clawith A2A |
|------|--------------|-------------|
| 跨机器 Agent 通信 | ✅ 原生支持（HTTP 中转） | ❌ 依赖 S3 共享存储 |
| 文件传输 | ❌ 无 | ✅ `send_file_to_agent` |
| 任务抽象 | ❌ 无（盲目对话） | ✅ Task + Focus Items |
| 权限体系 | ❌ 无 | ✅ L1/L2/L3 自主权限 |

两者互补：X-Client 补齐传输层，Clawith 补齐协作层。

### 1.2 设计目标

1. **保留 X-Client 的轻量优势**：MySQL 存储、Room-based 消息模型、@唤醒机制
2. **引入 Clawith 的协作能力**：Task 任务管理、L1/L2/L3 权限体系、S3 文件传输
3. **统一的工作区管理**：AgentCore 拥有独立工作区，X-Client 负责工作区同步
4. **向后兼容**：现有 X-Client 业务逻辑不受影响

---

## 2. 架构设计

### 2.1 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                         X-Client HTTP                            │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐   │
│  │  Message Router │  │  MemoryWindow    │  │  File Transfer  │   │
│  │  (Intent 路由)  │  │  (上下文管理)     │  │  (S3 Backend)  │   │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘   │
├─────────────────────────────────────────────────────────────────┤
│                    Collaboration Layer (X-Client)                 │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐  │
│  │  Permission Svc │  │  Intent Router  │  │  Workspace Mgr  │  │
│  │  (本地缓存)     │  │  (语义路由)     │  │  (工作区同步)   │  │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Coordinator (Hub)                           │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐   │
│  │  MySQL          │  │  S3 (File Store)│  │  Agent Registry │   │
│  │  (Messages,     │  │  (File Transfer)│  │                │   │
│  │   Tasks, Perms)│  │                │  │                │   │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      AgentCore (外部服务)                        │
│  ┌─────────────────┐  ┌─────────────────┐                       │
│  │  claude-agent   │  │  工作目录       │                       │
│  │  -sdk          │  │  /workspace     │                       │
│  │  (Python)      │  │  /memory        │                       │
│  └─────────────────┘  └─────────────────┘                       │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 数据流

```
X-Client A                                  Coordinator                     X-Client B
   │                                              │                              │
   │  @agent /delegate task: 完成报告              │                              │
   │ ───────────────────────────────────────────► │                              │
   │                                              │                              │
   │  [Intent = DELEGATE, task_id = nil]          │  POST /api/task              │
   │                                              │ ────────────────────────────►│
   │                                              │                              │
   │                                              │  ◄── { task_id: "xxx" }      │
   │                                              │                              │
   │  ◄── { task_created, task_id: "xxx" }        │                              │
   │                                              │                              │
   │  POST /api/task/xxx/focus (添加关注点)        │                              │
   │ ───────────────────────────────────────────► │                              │
   │                                              │                              │
   │  send_message(room_id, intent=DELEGATE,      │                              │
   │                 task_id="xxx")               │                              │
   │ ───────────────────────────────────────────► │                              │
   │                                              │  保存消息 (带 task_id)         │
   │                                              │ ────────────────────────────►│
   │                                              │                              │
   │                                              │  poll() ◄──────────────────── │
   │                                              │                              │
```

---

## 3. 工作区架构

### 3.1 设计原则

1. **统一目录管理**：X-Client 和 AgentCore 共用同一套目录结构，由 X-Client 统一管理
2. **文件传递透明化**：文件通过 S3 传递，X-Client 下载后写入工作区，AgentCore 可直接读取
3. **Session 隔离**：群聊使用独立 session_id，避免与 AgentCore 内部记忆冲突
4. **无需共享卷**：同一机器上无需挂载，直接通过文件系统访问

### 3.2 统一目录结构

**X-Client 和 AgentCore 共用同一套工作区目录**：

```
{DATA_DIR}/
└── {agent_id}/
    ├── config.json                 # X-Client 配置文件
    ├── memory.json                 # MemoryWindow 持久化（可选）
    ├── soul.md                     # Agent 人格定义
    ├── memory/
    │   ├── memory.md               # 长期记忆
    │   └── conversation_*.md       # 会话历史
    ├── workspace/                  # 工作区（AgentCore 使用）
    │   ├── uploads/                # 接收的文件
    │   │   └── {room_id}/
    │   │       └── {file_name}
    │   ├── inbox/                 # 收件箱（A2A 消息通知）
    │   │   └── messages/
    │   ├── reports/               # 报告产出（AgentCore 写入）
    │   └── temp/                  # 临时文件
    ├── downloads/                 # 下载缓存（S3 文件）
    │   └── {room_id}/
    │       └── {file_name}
    └── skills/                    # 技能定义
        └── *.md
```

**AgentCore 配置**：
```json
{
  "workspace_dir": "/data/{agent_id}/workspace",
  "memory_dir": "/data/{agent_id}/memory",
  "data_dir": "/data/{agent_id}"
}
```

### 3.3 Workspace Manager 模块

X-Client 负责工作区的读写管理：

```go
// x-client-http/workspacemanager.go

type WorkspaceManager struct {
    agentID      string
    dataDir      string  // X-Client 数据根目录
    s3Client    *S3Client
}

// NewWorkspaceManager 创建工作区管理器
func NewWorkspaceManager(agentID, dataDir string) *WorkspaceManager {
    return &WorkspaceManager{
        agentID: agentID,
        dataDir: dataDir,
    }
}

// GetWorkspacePath 获取工作区根路径
func (wm *WorkspaceManager) GetWorkspacePath() string {
    return filepath.Join(wm.dataDir, wm.agentID, "workspace")
}

// GetUploadsPath 获取上传文件目录
func (wm *WorkspaceManager) GetUploadsPath(roomID string) string {
    return filepath.Join(wm.dataDir, wm.agentID, "workspace", "uploads", roomID)
}

// GetInboxPath 获取收件箱目录
func (wm *WorkspaceManager) GetInboxPath() string {
    return filepath.Join(wm.dataDir, wm.agentID, "workspace", "inbox", "messages")
}

// GetDownloadsPath 获取下载缓存目录
func (wm *WorkspaceManager) GetDownloadsPath(roomID string) string {
    return filepath.Join(wm.dataDir, wm.agentID, "downloads", roomID)
}

// SaveFile 保存文件到工作区
func (wm *WorkspaceManager) SaveFile(roomID, fileName string, data []byte) error {
    dir := wm.GetUploadsPath(roomID)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return err
    }
    path := filepath.Join(dir, fileName)
    return os.WriteFile(path, data, 0644)
}

// ReadFile 读取工作区文件
func (wm *WorkspaceManager) ReadFile(relPath string) ([]byte, error) {
    fullPath := filepath.Join(wm.dataDir, wm.agentID, relPath)
    return os.ReadFile(fullPath)
}

// WriteInboxMessage 写入收件箱消息
func (wm *WorkspaceManager) WriteInboxMessage(msg string) error {
    dir := wm.GetInboxPath()
    if err := os.MkdirAll(dir, 0755); err != nil {
        return err
    }
    fileName := fmt.Sprintf("%d_inbox.md", time.Now().UnixNano())
    path := filepath.Join(dir, fileName)
    return os.WriteFile(path, []byte(msg), 0644)
}

// ListFiles 列出工作区文件
func (wm *WorkspaceManager) ListFiles(subDir string) ([]string, error) {
    fullPath := filepath.Join(wm.dataDir, wm.agentID, "workspace", subDir)
    entries, err := os.ReadDir(fullPath)
    if err != nil {
        return nil, err
    }
    var files []string
    for _, e := range entries {
        if !e.IsDir() {
            files = append(files, e.Name())
        }
    }
    return files, nil
}
```

### 3.4 文件传输完整流程

```
Agent A                          Coordinator                      S3                       Agent B
   │                                 │                              │                        │
   │  POST /file/upload/presign     │                              │                        │
   │ ──────────────────────────────►│                              │                        │
   │                                 │                              │                        │
   │  ◄── { presigned_url }        │                              │                        │
   │                                 │                              │                        │
   │  PUT { presigned_url }        │                              │                        │
   │─────────────────────────────────────────────────────────────►│                        │
   │                                 │                              │                        │
   │                                 │           200 OK           │                        │
   │                                 │◄─────────────────────────────│                        │
   │                                 │                              │                        │
   │  POST /transfer/complete       │                              │                        │
   │ ──────────────────────────────►│                              │                        │
   │                                 │                              │                        │
   │                                 │  1. Update DB: completed     │                        │
   │                                 │  2. SaveMessage (file)       │                        │
   │                                 │                              │                        │
   │  ◄── { msg_id }               │                              │                        │
   │                                 │                              │                        │
   │                            poll() ◄────────────────────────────────────────────────────│
   │                                 │                              │                        │
   │                            ◄── [msg: {type:file, transfer_id}]                       │
   │                                 │                              │                        │
   │  GET /file/download/presign    │                              │                        │
   │─────────────────────────────────────────────────────────────────────────────────────►│
   │                                 │                              │                        │
   │                            ◄── { presigned_url }                                      │
   │                                 │                              │                        │
   │  GET { presigned_url }         │                              │                        │
   │─────────────────────────────────────────────────────────────────────────────────────►│
   │                                 │                              │                        │
   │                            ◄── 200 OK (file)                                          │
   │                                 │                              │                        │
   │  ┌─────────────────────────────┐                              │                        │
   │  │ WorkspaceManager.SaveFile   │                              │                        │
   │  │ 保存到 workspace/uploads/   │                              │                        │
   │  │ {room_id}/{file_name}      │                              │                        │
   │  └─────────────────────────────┘                              │                        │
   │                                 │                              │                        │
   │  AgentCore 直接读取工作区文件                                  │                        │
```

### 3.5 Session 隔离策略

**问题**：AgentCore 有内部单聊记忆，X-Client 拼接的群聊历史可能与之冲突

**解决方案**：群聊使用独立 session_id

```go
// x-Client-http/main.go

type XClient struct {
    // ... 其他字段
    workspaceMgr *WorkspaceManager
}

func (x *XClient) buildChatRequest(msg *PollMessage) (*ChatRequest, error) {
    // 1. 获取 MemoryWindow 上下文
    memory := x.getMemoryWindow(msg.RoomID)
    context := memory.BuildContext(msg.Sender, msg.Content)

    // 2. 生成群聊专用 session_id（与 AgentCore 内部记忆隔离）
    sessionID := x.sessionMgr.GenerateGroupSessionID(msg.RoomID)

    // 3. 构建请求
    chatReq := &ChatRequest{
        Message:   context,
        SessionID: sessionID,  // 群聊 session，AgentCore 不加载历史
        Sender:    msg.Sender,
        RoomID:    msg.RoomID,
        TaskID:    msg.TaskID,  // 关联任务（可选）
    }

    return chatReq, nil
}

func (sm *SessionManager) GenerateGroupSessionID(roomID string) string {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    sm.counter++
    return fmt.Sprintf("group_%s_%d_%d",
        roomID,
        time.Now().UnixNano(),
        sm.counter)
}
```

### 3.6 AgentCore Inbox 通知机制

**问题**：Coordinator 创建 inbox 消息后，AgentCore 如何感知？

**方案**：X-Client 轮询 WorkspaceManager.Inbox，检测到新文件时通知 AgentCore

```go
// x-client-http/inbox_watcher.go

// StartInboxWatcher 启动 inbox 监听
func (x *XClient) StartInboxWatcher(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            x.checkInbox()
        }
    }
}

// checkInbox 检查 inbox 目录是否有新消息
func (x *XClient) checkInbox() error {
    inboxPath := x.workspaceMgr.GetInboxPath()
    entries, err := os.ReadDir(inboxPath)
    if err != nil {
        if os.IsNotExist(err) {
            return nil
        }
        return err
    }

    for _, entry := range entries {
        if entry.IsDir() {
            continue
        }
        fileName := entry.Name()

        // 忽略已处理的消息
        if x.isInboxProcessed(fileName) {
            continue
        }

        // 读取并处理消息
        fullPath := filepath.Join(inboxPath, fileName)
        data, err := os.ReadFile(fullPath)
        if err != nil {
            log.Printf("[WARN] 读取 inbox 消息失败: %v", err)
            continue
        }

        // 解析消息内容
        var inboxMsg InboxMessage
        if err := json.Unmarshal(data, &inboxMsg); err != nil {
            log.Printf("[WARN] 解析 inbox 消息失败: %v", err)
            continue
        }

        // 通知 AgentCore（有新任务/文件）
        if err := x.notifyAgentCore(inboxMsg); err != nil {
            log.Printf("[WARN] 通知 AgentCore 失败: %v", err)
            continue
        }

        // 标记为已处理（可移动到 processed/ 目录或删除）
        x.markInboxProcessed(fileName)
        log.Printf("[INFO] inbox 消息已处理: %s", fileName)
    }

    return nil
}

// isInboxProcessed 检查 inbox 消息是否已处理
func (x *XClient) isInboxProcessed(fileName string) bool {
    processedDir := filepath.Join(x.workspaceMgr.GetInboxPath(), "processed")
    processedFile := filepath.Join(processedDir, fileName)
    _, err := os.Stat(processedFile)
    return err == nil
}

// markInboxProcessed 标记 inbox 消息为已处理
func (x *XClient) markInboxProcessed(fileName string) error {
    processedDir := filepath.Join(x.workspaceMgr.GetInboxPath(), "processed")
    if err := os.MkdirAll(processedDir, 0755); err != nil {
        return err
    }

    srcPath := filepath.Join(x.workspaceMgr.GetInboxPath(), fileName)
    destPath := filepath.Join(processedDir, fileName)
    return os.Rename(srcPath, destPath)
}

// notifyAgentCore 通知 AgentCore 有新消息
func (x *XClient) notifyAgentCore(inboxMsg InboxMessage) error {
    // 方案1: 直接调用 AgentCore API（如果支持）
    // 方案2: 写入特殊文件触发 AgentCore 重新读取
    // 这里使用方案1的简化实现
    log.Printf("[INFO] [%s] 通知 AgentCore: type=%s, task_id=%s", x.agentID, inboxMsg.Type, inboxMsg.TaskID)
    return nil
}

type InboxMessage struct {
    Type      string `json:"type"`       // "file", "delegate", "notify"
    TaskID    string `json:"task_id,omitempty"`
    FileName  string `json:"file_name,omitempty"`
    FromAgent string `json:"from_agent"`
    RoomID    string `json:"room_id"`
    Content   string `json:"content"`
    Timestamp int64  `json:"timestamp"`
}
```

**触发时机**：
1. Coordinator 保存文件消息后，X-Client poll 拿到 `type=file` 消息
2. X-Client 下载文件并 SaveFile 到 `workspace/uploads/{room_id}/`
3. X-Client 同时可能写入 inbox 消息（如果需要通知 AgentCore 新任务）
4. AgentCore 通过文件系统变化或定时扫描感知新文件

**简化方案**（推荐）：
- 直接在 X-Client 处理消息时同步通知 AgentCore，无需 inbox 目录轮询
- inbox 目录作为备用机制，处理离线场景

---

## 4. 核心模块

### 4.1 Intent Router（意图路由）

扩展 X-Client 的消息 Intent，支持更多语义：

| Intent | 说明 | 触发条件 |
|--------|------|---------|
| `INFORM` | 通知 | 默认 |
| `REQUEST` | 请求 | 被 @ 且需要回复 |
| `RESPONSE` | 回复 | 作为 REQUEST 的响应 |
| `DELEGATE` | 委托任务 | `@agent /delegate task: xxx` |
| `QUERY` | 查询状态 | `@agent /query task: xxx` |

**消息结构扩展**：

```go
type PollMessage struct {
    MsgID        string   `json:"msg_id"`
    RoomID       string   `json:"room_id"`
    SenderID     string   `json:"sender_id"`
    SenderType   string   `json:"sender_type"`  // agent / user
    Content      string   `json:"content"`
    MentionUsers []string `json:"mention_users"`
    Intent       string   `json:"intent"`       // INFORM / REQUEST / RESPONSE / DELEGATE / QUERY
    ReplyToMsgID string   `json:"reply_to_msg_id,omitempty"`
    TargetID     string   `json:"target_id"`
    CreatedAt    int64    `json:"created_at"`

    // 新增字段
    TaskID       string   `json:"task_id,omitempty"`       // 关联任务
    FocusItems   []FocusItem `json:"focus_items,omitempty"` // 关注点
}
```

### 4.2 Task Manager（任务管理）

**目标**：让 Agent 之间的对话围绕任务展开，而非盲目闲聊。

**Task 模型**：

```go
type Task struct {
    TaskID       string    `json:"task_id"`       // UUID
    Title        string    `json:"title"`
    Description  string    `json:"description"`
    Status       string    `json:"status"`         // todo / in_progress / done
    Priority     int       `json:"priority"`       // 1-5

    CreatedBy    string    `json:"created_by"`     // agent_id
    AssignedTo   string    `json:"assigned_to"`    // agent_id

    RoomID       string    `json:"room_id"`        // 关联的 Room
    ParentTaskID string    `json:"parent_task_id,omitempty"` // 父任务（可选）

    CreatedAt    int64     `json:"created_at"`
    UpdatedAt    int64     `json:"updated_at"`
    CompletedAt  int64     `json:"completed_at,omitempty"`
}
```

**Focus Item 模型**：

```go
type FocusItem struct {
    ItemID    string   `json:"item_id"`
    TaskID    string   `json:"task_id"`
    Content   string   `json:"content"`    // 如: "[ ] 设计 API 文档"
    Status    string   `json:"status"`     // "[ ]" / "[/]" / "[x]"
    AgentID   string   `json:"agent_id"`   // 负责的 Agent
    RoomID    string   `json:"room_id"`
    Order     int      `json:"order"`      // 排序
}
```

### 4.3 Permission Service（权限服务）

**三级权限体系**：

```go
const (
    PermissionL1 = "l1"  // 读操作、低风险：read_file, list_dir, search
    PermissionL2 = "l2"  // 写操作、中风险：write_file, send_message, create_task
    PermissionL3 = "l3"  // 高风险：delete_file, execute_code, transfer_fund
)
```

**权限配置模型**：

```go
type AgentPermission struct {
    AgentID       string   `json:"agent_id"`
    Level         string   `json:"level"`            // l1 / l2 / l3
    AllowedTools  []string `json:"allowed_tools"`   // 白名单
    DeniedTools   []string `json:"denied_tools"`    // 黑名单

    // 配额控制
    Quota         *Quota   `json:"quota,omitempty"`
}

type Quota struct {
    DailyTokenLimit    int64 `json:"daily_token_limit"`
    MonthlyTokenLimit  int64 `json:"monthly_token_limit"`
    FileSizeLimitMB    int   `json:"file_size_limit_mb"`
    MessageLimitPerHour Int  `json:"message_limit_per_hour"`
}
```

**权限加载方式：集中管理 + 本地缓存**

```
┌─────────────────────────────────────────────────────────────────┐
│                      X-Client 端                                 │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │  PermissionCache (内存缓存)                                   ││
│  │  - Level: "l2"                                               ││
│  │  - AllowedTools: [...]                                      ││
│  │  - DeniedTools: [...]                                       ││
│  │  - Quota: {...}                                             ││
│  │  - ExpiresAt: 2024-01-01 12:00:00                          ││
│  └─────────────────────────────────────────────────────────────┘│
│                          ▲                                       │
│                          │ 启动时拉取 / 缓存过期时刷新             │
│                          │                                       │
└──────────────────────────┼───────────────────────────────────────┘
                           │
                           │ GET /api/permission/{agent_id}
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Coordinator (Hub)                            │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │  MySQL: agent_permissions 表                                ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

**X-Client 端实现**：

```go
// x-client-http/permission.go

type PermissionCache struct {
    permission AgentPermission
    expiresAt  time.Time
    mu         sync.RWMutex
}

const (
    PermissionCacheTTL = 5 * time.Minute  // 缓存 5 分钟
)

func (x *XClient) loadPermissions() error {
    resp, err := x.httpClient.Get(x.coordinatorURL + "/api/permission/" + x.agentID)
    if err != nil {
        return fmt.Errorf("加载权限失败: %w", err)
    }
    defer resp.Body.Close()

    var perm AgentPermission
    if err := json.NewDecoder(resp.Body).Decode(&perm); err != nil {
        return err
    }

    x.permCache.Set(perm, time.Now().Add(PermissionCacheTTL))
    return nil
}

func (x *XClient) checkPermission(action string) error {
    perm, err := x.getPermission()
    if err != nil {
        return err
    }

    // 工具黑名单检查
    for _, denied := range perm.DeniedTools {
        if action == denied {
            return fmt.Errorf("工具 %s 在黑名单中", action)
        }
    }

    // 工具白名单检查
    if len(perm.AllowedTools) > 0 {
        allowed := false
        for _, allowedTool := range perm.AllowedTools {
            if action == allowedTool {
                allowed = true
                break
            }
        }
        if !allowed {
            return fmt.Errorf("工具 %s 不在白名单中", action)
        }
    }

    // 级别检查
    requiredLevel := getRequiredLevel(action)
    if compareLevel(perm.Level, requiredLevel) < 0 {
        return fmt.Errorf("权限级别不足，需要 %s，当前 %s", requiredLevel, perm.Level)
    }

    return nil
}
```

### 4.4 File Transfer（S3 文件传输）

**设计目标**：Agent 之间可以传递文件制品，支持跨机器传输。

**File Transfer 模型**：

```go
type FileTransfer struct {
    TransferID   string    `json:"transfer_id"`
    FileName    string    `json:"file_name"`
    FileSize    int64     `json:"file_size"`
    MimeType    string    `json:"mime_type"`

    FromAgent   string    `json:"from_agent"`
    ToAgent     string    `json:"to_agent"`

    RoomID      string    `json:"room_id"`         // 关联的 Room
    TaskID      string    `json:"task_id,omitempty"` // 关联的任务（可选）

    S3Key       string    `json:"s3_key"`          // S3 对象 key
    Status      string    `json:"status"`          // pending / uploading / completed / failed

    CreatedAt   int64     `json:"created_at"`
    CompletedAt int64     `json:"completed_at,omitempty"`
}
```

**X-Client 端文件处理**：

```go
// x-client-http/filetransfer.go

func (x *XClient) handleFileMessage(msg *PollMessage) error {
    transferID := msg.Extra["transfer_id"]
    fileName := msg.Extra["file_name"]
    roomID := msg.RoomID

    // 1. 检查权限（文件传输需要 L2）
    if err := x.checkPermission("file_transfer"); err != nil {
        return fmt.Errorf("权限不足: %w", err)
    }

    // 2. 获取下载 presigned URL
    presignResp, err := x.getDownloadPresign(transferID)
    if err != nil {
        return fmt.Errorf("获取下载链接失败: %w", err)
    }

    // 3. 下载文件
    data, err := x.downloadFileData(presignResp)
    if err != nil {
        return fmt.Errorf("下载文件失败: %w", err)
    }

    // 4. 保存到工作区（AgentCore 直接可读）
    if err := x.workspaceMgr.SaveFile(roomID, fileName, data); err != nil {
        return fmt.Errorf("保存文件到工作区失败: %w", err)
    }

    log.Printf("[INFO] [%s] 文件已接收并保存到工作区: %s/%s", x.agentID, roomID, fileName)
    return nil
}

// downloadFileData 下载文件数据（流式处理，适合大文件）
// PresignDownloadResponse 下载 presigned URL 响应
// 响应格式: { "presigned_url": "https://...", "room_id": "xxx" }

func (x *XClient) downloadFileData(presignResp *PresignDownloadResponse) ([]byte, error) {
    resp, err := x.httpClient.Get(presignResp.PresignedURL)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("S3 下载失败: %d", resp.StatusCode)
    }

    // 流式写入临时文件，避免大文件占用内存
    tmpDir := x.workspaceMgr.GetDownloadsPath(presignResp.RoomID)
    if err := os.MkdirAll(tmpDir, 0755); err != nil {
        return nil, err
    }

    tmpFile, err := os.CreateTemp(tmpDir, "dl_")
    if err != nil {
        return nil, err
    }
    defer tmpFile.Close()

    if _, err := io.Copy(tmpFile, resp.Body); err != nil {
        return nil, err
    }

    // 读取并返回
    tmpFile.Seek(0, 0)
    return io.ReadAll(tmpFile)
}

// SaveFileFromPath 保存文件到工作区（给定源文件路径）
func (wm *WorkspaceManager) SaveFileFromPath(roomID, fileName, srcPath string) error {
    dir := wm.GetUploadsPath(roomID)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return err
    }
    destPath := filepath.Join(dir, fileName)
    return copyFile(srcPath, destPath)
}

// ReadReport 读取 AgentCore 产出的报告
func (wm *WorkspaceManager) ReadReport(roomID string) ([]byte, error) {
    reportsPath := filepath.Join(wm.dataDir, wm.agentID, "workspace", "reports", roomID)
    entries, err := os.ReadDir(reportsPath)
    if err != nil {
        return nil, err
    }
    // 返回最新报告
    var latest os.FileInfo
    for _, e := range entries {
        if e.IsDir() {
            continue
        }
        info, _ := e.Info()
        if latest == nil || info.ModTime().After(latest.ModTime()) {
            latest = info
        }
    }
    if latest == nil {
        return nil, fmt.Errorf("未找到报告")
    }
    return os.ReadFile(filepath.Join(reportsPath, latest.Name()))
}
```

---

## 5. 数据库设计

### 5.1 存储说明

- **数据库**：MySQL（与现有 coordinator-http 一致）
- **连接驱动**：`github.com/go-sql-driver/mysql`
- **ORM**：可选使用 GORM（与 storage 目录下的 MySQLStorage 一致）

### 5.2 新增表

```sql
-- Task 表
CREATE TABLE tasks (
    id             BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    task_id        VARCHAR(64) NOT NULL UNIQUE,
    title          TEXT NOT NULL,
    description    TEXT,
    status         VARCHAR(32) NOT NULL DEFAULT 'todo',  -- todo / in_progress / done
    priority       INT NOT NULL DEFAULT 3,                 -- 1-5

    created_by     VARCHAR(64) NOT NULL,
    assigned_to    VARCHAR(64) NOT NULL,
    room_id        VARCHAR(64) NOT NULL,

    parent_task_id VARCHAR(64),

    created_at     BIGINT UNSIGNED NOT NULL,
    updated_at     BIGINT UNSIGNED NOT NULL,
    completed_at   BIGINT UNSIGNED,

    INDEX idx_tasks_room_id (room_id),
    INDEX idx_tasks_assigned_to (assigned_to),
    INDEX idx_tasks_created_by (created_by),
    INDEX idx_tasks_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Focus Items 表
CREATE TABLE focus_items (
    id             BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    item_id        VARCHAR(64) NOT NULL UNIQUE,
    task_id        VARCHAR(64) NOT NULL,
    content        TEXT NOT NULL,
    status         VARCHAR(8) NOT NULL DEFAULT '[ ]',   -- [ ] / [/] / [x]
    agent_id       VARCHAR(64) NOT NULL,
    room_id        VARCHAR(64) NOT NULL,
    item_order     INT NOT NULL DEFAULT 0,

    created_at     BIGINT UNSIGNED NOT NULL,
    updated_at     BIGINT UNSIGNED NOT NULL,

    INDEX idx_focus_task_id (task_id),
    INDEX idx_focus_agent_id (agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Agent Permissions 表
CREATE TABLE agent_permissions (
    id             BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    agent_id       VARCHAR(64) NOT NULL UNIQUE,
    level          VARCHAR(8) NOT NULL DEFAULT 'l1',    -- l1 / l2 / l3
    allowed_tools  TEXT,                              -- JSON array
    denied_tools   TEXT,                              -- JSON array

    daily_token_limit    BIGINT UNSIGNED,
    monthly_token_limit  BIGINT UNSIGNED,
    file_size_limit_mb   INT,
    message_limit_per_hour INT,

    created_at     BIGINT UNSIGNED NOT NULL,
    updated_at     BIGINT UNSIGNED NOT NULL,

    INDEX idx_perm_agent_id (agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- File Transfers 表
CREATE TABLE file_transfers (
    id             BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    transfer_id    VARCHAR(64) NOT NULL UNIQUE,
    file_name      TEXT NOT NULL,
    file_size      BIGINT UNSIGNED NOT NULL,
    mime_type      VARCHAR(128),

    from_agent     VARCHAR(64) NOT NULL,
    to_agent       VARCHAR(64),

    room_id        VARCHAR(64) NOT NULL,
    task_id        VARCHAR(64),

    s3_key         TEXT NOT NULL,
    status         VARCHAR(32) NOT NULL DEFAULT 'pending', -- pending / uploading / completed / failed

    created_at     BIGINT UNSIGNED NOT NULL,
    completed_at   BIGINT UNSIGNED,

    INDEX idx_transfer_room_id (room_id),
    INDEX idx_transfer_from_agent (from_agent),
    INDEX idx_transfer_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Messages 表扩展：增加 task_id
ALTER TABLE messages ADD COLUMN task_id VARCHAR(64) NULL;
CREATE INDEX idx_messages_task_id ON messages(task_id);
```

### 5.3 S3 配置

```go
type S3Config struct {
    Bucket           string `json:"bucket"`
    Region           string `json:"region"`
    AccessKeyID      string `json:"access_key_id"`
    SecretAccessKey  string `json:"secret_access_key"`
    Endpoint         string `json:"endpoint,omitempty"`  // 兼容 MinIO 等
    PresignExpiryMin int    `json:"presign_expiry_min"` // 默认 30 分钟
}
```

---

## 6. API 设计

### 6.1 Task API

| 方法 | 路径 | 说明 | 归属 |
|------|------|------|------|
| POST | `/api/task` | 创建任务 | Coordinator |
| GET | `/api/task/{task_id}` | 获取任务详情 | Coordinator |
| PUT | `/api/task/{task_id}` | 更新任务 | Coordinator |
| DELETE | `/api/task/{task_id}` | 删除任务 | Coordinator |
| GET | `/api/task/room/{room_id}` | 获取 Room 下的任务 | Coordinator |
| GET | `/api/task/agent/{agent_id}` | 获取 Agent 被分配的任务 | Coordinator |

### 6.2 Focus API

| 方法 | 路径 | 说明 | 归属 |
|------|------|------|------|
| POST | `/api/task/{task_id}/focus` | 添加关注点 | Coordinator |
| PUT | `/api/focus/{item_id}` | 更新关注点状态 | Coordinator |
| DELETE | `/api/focus/{item_id}` | 删除关注点 | Coordinator |
| GET | `/api/task/{task_id}/focus` | 获取任务的所有关注点 | Coordinator |

### 6.3 Permission API

| 方法 | 路径 | 说明 | 归属 |
|------|------|------|------|
| GET | `/api/permission/{agent_id}` | 获取 Agent 权限 | Coordinator |
| POST | `/api/permission` | 创建/更新 Agent 权限 | Coordinator |
| DELETE | `/api/permission/{agent_id}` | 删除权限配置 | Coordinator |

### 6.4 File Transfer API

| 方法 | 路径 | 说明 | 归属 |
|------|------|------|------|
| POST | `/api/file/upload/presign` | 获取上传 Presigned URL | Coordinator |
| POST | `/api/transfer/complete` | 标记上传完成 | Coordinator |
| GET | `/api/file/download/presign` | 获取下载 Presigned URL | Coordinator |
| GET | `/api/transfer/{transfer_id}` | 查询传输状态 | Coordinator |
| GET | `/api/transfer/room/{room_id}` | 获取 Room 下的传输记录 | Coordinator |

---

## 7. 改动归属

### 7.1 Coordinator 端改动（`coordinator-http`）

```
1. 数据库层 (storage.go)
   - 新增 CreateTask, GetTask, UpdateTask, DeleteTask
   - 新增 CreateFocusItem, GetFocusItems, UpdateFocusItem, DeleteFocusItem
   - 新增 GetPermission, UpsertPermission, DeletePermission
   - 新增 CreateFileTransfer, GetFileTransfer, UpdateFileTransferStatus
   - 新增 PollMessages 扩展 task_id 字段

2. Handler 层 (handler.go)
   - Task CRUD: POST/GET/PUT/DELETE /api/task/*
   - Focus: POST/GET/PUT/DELETE /api/focus/*
   - Permission: GET/POST/DELETE /api/permission/*
   - File: POST /api/file/upload/presign
   - Transfer: POST /api/transfer/complete, GET /api/file/download/presign

3. S3 集成 (新增 s3.go)
   - S3 客户端封装
   - Presigned URL 生成
```

### 7.2 X-Client 端改动（`x-client-http`）

```
1. 模型层 (models.go)
   - 新增 Task、FocusItem 模型
   - 扩展 PollMessage（增加 task_id 字段）
   - 新增 FileTransfer 模型

2. Workspace Manager (新增 workspacemanager.go)
   - GetWorkspacePath() / GetUploadsPath() / GetInboxPath()
   - SaveFile() 保存文件到工作区
   - ReadFile() 读取工作区文件
   - WriteInboxMessage() 写入收件箱消息
   - ListFiles() 列出工作区文件

3. Permission 服务 (新增 permission.go)
   - PermissionCache 本地缓存
   - loadPermissions() 启动时拉取
   - checkPermission() 权限检查

4. Intent 路由 (main.go 或新增 intent.go)
   - ParseIntent() 解析命令格式
   - ParseDelegateCommand() 解析委托命令
   - handleTaskDelegate() 处理 DELEGATE
   - handleTaskQuery() 处理 QUERY

5. 文件传输 (新增 filetransfer.go)
   - getUploadPresign() / getDownloadPresign()
   - uploadFile() 上传到 S3
   - downloadFile() 从 S3 下载
   - handleFileMessage() 处理收到的文件消息
```

---

## 8. 实施计划

### Phase 1：数据库与基础设施（1-2 天）

- [ ] **Coordinator**: 创建 Task、Focus Item、Permission、FileTransfer 表
- [ ] **Coordinator**: 实现 S3 客户端封装（上传、下载、Presigned URL）
- [ ] **Coordinator**: 配置管理（支持 S3 配置）
- [ ] **配置**: MySQL 连接复用现有 `storage.go`

### Phase 2：Task & Focus 系统（2-3 天）

- [ ] **Coordinator**: Task CRUD API 实现
- [ ] **Coordinator**: Focus Item API 实现
- [ ] **Coordinator**: Room-Task 关联查询
- [ ] **X-Client**: 解析 `/delegate` 命令
- [ ] **X-Client**: 消息携带 task_id

### Phase 3：Permission 服务（1-2 天）

- [ ] **Coordinator**: Permission API 实现
- [ ] **X-Client**: PermissionCache 实现
- [ ] **X-Client**: checkPermission() 集成到消息发送流程
- [ ] **测试**: 权限检查端到端验证

### Phase 4：Intent 路由增强（1 天）

- [ ] **X-Client**: 扩展 PollMessage，增加 TaskID
- [ ] **X-Client**: DELEGATE / QUERY Intent 处理
- [ ] **X-Client**: 消息与 Task 的绑定

### Phase 5：File Transfer + Workspace Manager（2-3 天）

- [ ] **Coordinator**: Presigned URL 生成 API
- [ ] **Coordinator**: 上传完成回调
- [ ] **Coordinator**: Room 消息集成（文件消息类型）
- [ ] **X-Client**: WorkspaceManager 实现
- [ ] **X-Client**: 文件上传/下载实现
- [ ] **X-Client**: handleFileMessage() 调用 SaveFile 保存到工作区
- [ ] **X-Client**: AgentCore 产出报告读取（ReadReport）
- [ ] **测试**: Agent A → Coordinator → Agent B → AgentCore 完整流程

### Phase 6：测试与优化（1-2 天）

- [ ] 单元测试
- [ ] 端到端测试
- [ ] 性能优化（批量查询、缓存）

---

## 附录

### A. 与 Clawith 的模块对应关系

| Clawith 模块 | X-Client 实现 |
|-------------|--------------|
| `CollaborationService` | `Intent Router` + `Collaboration Layer` |
| `Task` | `tasks` 表 |
| `Focus Item` | `focus_items` 表 |
| `AgentPermission` (L1/L2/L3) | `agent_permissions` 表 + `Permission Svc` |
| `send_file_to_agent` | `FileTransfer` + S3 + `WorkspaceManager.SaveFile` |
| `ChatSession` | `Room` + `messages` 表 |
| `Trigger` | Agent Polling（原生支持） |
| `A2AContext` | `PollMessage` + 扩展字段 |
| `workspace/inbox/files` | `{data_dir}/{agent_id}/workspace/uploads/{room_id}/` |
| `Storage Runtime` | `WorkspaceManager` |
| `Agent Storage (共享)` | 统一目录，无需共享卷 |

### B. 向后兼容

- 现有 X-Client 消息模型（无 TaskID）仍正常工作
- 新增字段（TaskID、FocusItems）在无值时为 nil/null，不影响旧逻辑
- Permission 检查默认放行（level=l1），不影响现有 Agent

### C. 配置示例

**coordinator-http/config.json**：

```json
{
  "host": "0.0.0.0",
  "port": 8080,
  "mysql": {
    "host": "localhost",
    "port": 3306,
    "user": "root",
    "password": "xxx",
    "database": "coordinator"
  },
  "s3": {
    "bucket": "x-client-files",
    "region": "us-east-1",
    "access_key_id": "xxx",
    "secret_access_key": "xxx",
    "endpoint": "",
    "presign_expiry_min": 30
  }
}
```

**x-client-http/config.json**：

```json
{
  "agent_id": "agent_a",
  "coordinator_url": "http://localhost:8080",
  "agentcore_url": "http://localhost:8000",
  "listen_addr": ":8001",
  "endpoint": "http://localhost:8001",
  "poll_interval": 5,
  "poll_batch_size": 50,
  "heartbeat_interval": 30,
  "max_memory_size": 50,
  "max_memory_chars": 2000,
  "permission_cache_ttl_min": 5,
  "data_dir": "/data"
}
```

### D. Permission 级别速查表

| 级别 | 权限 | 允许的操作 |
|------|------|-----------|
| L1 | 只读 | `read_file`, `list_dir`, `search`, `poll` |
| L2 | 普通写 | L1 + `write_file`, `send_message`, `create_task`, `delegate_task`, `file_transfer` |
| L3 | 高风险 | L2 + `delete_file`, `execute_code`, `transfer_fund` |

### E. 目录结构速查

**统一目录（X-Client 和 AgentCore 共用）**：

```
{DATA_DIR}/{agent_id}/
├── config.json                 # X-Client 配置
├── soul.md                     # Agent 人格定义
├── memory/
│   ├── memory.json             # MemoryWindow 持久化
│   └── conversation_*.md      # 会话历史
├── workspace/                  # 工作区（AgentCore 使用）
│   ├── uploads/{room_id}/     # 接收的文件
│   ├── inbox/messages/        # A2A 消息通知
│   ├── reports/               # AgentCore 产出报告
│   └── temp/                  # 临时文件
├── downloads/                  # S3 下载缓存
│   └── {room_id}/{file_name}
└── skills/                    # 技能定义
    └── *.md
```

**AgentCore 启动时指定**：
```bash
agentcore --workspace-dir=/data/{agent_id}/workspace
```

### F. 关键设计决策

1. **统一目录管理**：X-Client 和 AgentCore 共用同一套目录，由 X-Client 管理，AgentCore 无需共享卷挂载
2. **Session 隔离**：群聊使用独立 session_id（`group_{room_id}_{timestamp}_{counter}`），避免与 AgentCore 内部记忆冲突
3. **文件保存时机**：X-Client 收到文件后直接保存到工作区（`workspace/uploads/{room_id}/`），AgentCore 可直接读取
4. **权限缓存 TTL**：5 分钟，减少 API 调用
5. **S3 直传**：文件不经过 Coordinator，使用 Presigned URL 直传
