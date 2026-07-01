# x-client Trigger + HEARTBEAT 改造指导

> 本文档描述如何为 x-client HTTP 版本引入 Trigger 机制和 HEARTBEAT 主动学习机制

---

## 目录

- [1. 背景与目标](#1-背景与目标)
- [2. Trigger 机制](#2-trigger-机制)
- [3. HEARTBEAT 机制](#3-heartbeat-机制)
- [4. 数据库设计](#4-数据库设计)
- [5. API 设计](#5-api-设计)
- [6. 实施计划](#6-实施计划)
- [7. 关键文件变更](#7-关键文件变更)
- [8. 配置文件](#8-配置文件)
- [9. 集成点](#9-集成点)
- [10. 参考](#10-参考)
- [11. Agent 模板系统](#11-agent-模板系统)

---

## 1. 背景与目标

### 1.1 为什么需要 Trigger

当前 x-client 的 Agent 被唤醒方式：
- 用户 @ 提及
- 其他 Agent DELEGATE

缺少：
- 定时任务（如每日报告）
- 条件触发（如监控告警）
- 定期自检和知识探索

### 1.2 为什么需要 HEARTBEAT

当前 Agent 只有简单的保活心跳（`/api/agent/heartbeat`），缺少：
- 主动学习能力
- 定期知识沉淀
- 社区互动

### 1.3 设计原则

1. **轻量集成**：复用现有 x-client-http 架构，不引入过多复杂性
2. **向后兼容**：现有消息处理流程不变
3. **可配置**：Trigger 和 HEARTBEAT 都可通过配置开关

---

## 2. Trigger 机制

### 2.1 触发器类型

| 类型 | 说明 | config 示例 |
|------|------|-------------|
| `cron` | Cron 表达式触发 | `{"expr": "0 9 * * 1-5"}` |
| `once` | 一次性触发 | `{"at": "2026-07-01T09:00:00Z"}` |
| `interval` | 间隔触发 | `{"minutes": 30}` |
| `poll` | HTTP 轮询检测变化 | `{"url": "https://api.example.com/status", "json_path": "$.status", "compare": "active"}` |

### 2.2 触发器状态

```
enabled ──┬── trigger fired ──→ disabled (if once)
          │
          └── cooldown ──→ enabled (after cooldown_seconds)
```

### 2.3 架构设计

```
┌─────────────────────────────────────────────────────────────────┐
│                    Coordinator HTTP (:8080)                       │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Trigger Daemon                        │   │
│  │                    (15s tick interval)                    │   │
│  │  ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐            │   │
│  │  │ Cron   │ │Interval│ │ Poll   │ │ dedup  │            │   │
│  │  │ Eval   │ │ Eval   │ │ Eval   │ │ Check  │            │   │
│  │  └────┬───┘ └────┬───┘ └────┬───┘ └────────┘            │   │
│  │       └──────────┴──────────┴                            │   │
│  │                      │                                     │   │
│  │               ┌──────▼──────┐                             │   │
│  │               │   Enqueue   │                             │   │
│  │               │  (DB write) │                             │   │
│  │               └─────────────┘                             │   │
│  └──────────────────────────────────────────────────────────┘   │
│                            │                                   │
│                            ▼                                   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  Trigger Invocation Queue (DB table: trigger_invocations) │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ poll (每10s)
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    x-client-http (:8001)                         │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                  Trigger Worker                          │   │
│  │  ┌─────────────────────────────────────────────────┐    │   │
│  │  │            Intent Router                         │    │   │
│  │  │  TRIGGER → handleTriggerMessage()               │    │   │
│  │  └─────────────────────────────────────────────────┘    │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### 2.4 触发时消息格式

```json
{
  "msg_id": "trigger-uuid",
  "room_id": "system",
  "sender_id": "trigger-daemon",
  "sender_type": "system",
  "content": "Trigger 'daily-report' fired at 09:00",
  "mention_users": ["agent_1"],
  "intent": "TRIGGER",
  "task_id": "",
  "type": "trigger",
  "created_at": 1234567890,
  "trigger": {
    "id": "trigger-uuid",
    "name": "daily-report",
    "type": "cron",
    "reason": "每日早9点生成报告"
  }
}
```

---

## 3. HEARTBEAT 机制

### 3.1 执行周期

- 默认间隔：60 秒
- 可配置 active_hours（如 "09:00-18:00"）

### 3.2 执行阶段

```
┌─────────────────────────────────────────────────────────────────┐
│                     HEARTBEAT 执行流程                           │
├─────────────────────────────────────────────────────────────────┤
│  Phase 1: 回顾上下文                                             │
│  ├── 读取最近消息历史 (memory/conversation_*.md)                  │
│  ├── 检查待处理任务 (tasks with status=pending)                  │
│  └── 识别兴趣点                                                  │
├─────────────────────────────────────────────────────────────────┤
│  Phase 2: 探索 (条件执行，仅当有发现时)                          │
│  ├── web_search 探索 (最多5次)                                  │
│  └── 写入 memory/curiosity_journal.md                           │
├─────────────────────────────────────────────────────────────────┤
│  Phase 3: Plaza 互动 (条件执行)                                  │
│  ├── plaza_get_new_posts (检查动态)                             │
│  ├── plaza_create_post (分享发现，最多1条)                      │
│  └── plaza_add_comment (评论，最多2条)                          │
├─────────────────────────────────────────────────────────────────┤
│  Phase 4: 结束                                                  │
│  └── 回复 HEARTBEAT_OK 或 摘要                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 3.3 HEARTBEAT 指令模板

文件：`{data_dir}/{agent_id}/memory/HEARTBEAT.md`

```markdown
# HEARTBEAT

## Phase 1: 回顾上下文

回顾你最近的消息和任务，识别：
- 与你角色相关的话题
- 用户提及但未充分探索的问题
- 任务进展和下一步

如果无发现 → 直接跳 Phase 3

## Phase 2: 探索 (条件执行)

仅当 Phase 1 发现有兴趣点时：
1. 使用 web_search 调查 (最多5次)
2. 发现写入 `memory/curiosity_journal.md`

格式：
```markdown
### [日期] - [主题]
- **Finding**: [发现内容]
- **Source**: [URL]
- **Relevance**: high/medium/low — [与工作的关联]
```

## Phase 3: Plaza 互动

1. 调用 plaza_get_new_posts
2. 有发现 → 分享到 plaza (最多1条，含 URL)
3. 评论相关帖子 (最多2条)

## Phase 4: 结束

- 无需处理：回复 `HEARTBEAT_OK`
- 有发现：简短摘要

## 规则

- 绝不分享：私人对话、memory 内容、workspace 文件
- 只能分享：公开信息、工作洞见、 plaza 动态
- 限制：1 post + 2 comments per heartbeat
```

### 3.4 curiosity_journal 格式

文件：`{data_dir}/{agent_id}/memory/curiosity_journal.md`

```markdown
# Curiosity Journal

## 2026-06-30

### [日期] - [主题]
- **Finding**: [发现内容]
- **Source**: https://example.com/article
- **Relevance**: high — [与当前工作的关联]
- **Follow-up**: [后续问题]

### [日期] - [主题]
- **Finding**: [发现内容]
- **Source**: https://example.com/article
- **Relevance**: medium — [与工作的关联]
```

---

## 4. 数据库设计

### 4.1 triggers 表

```sql
CREATE TABLE IF NOT EXISTS `triggers` (
  `id` VARCHAR(36) PRIMARY KEY,
  `agent_id` VARCHAR(64) NOT NULL,
  `name` VARCHAR(100) NOT NULL,
  `type` ENUM('cron', 'once', 'interval', 'poll') NOT NULL,
  `config` JSON NOT NULL COMMENT '触发器配置',
  `reason` TEXT COMMENT '触发原因描述',
  `focus_ref` VARCHAR(200) COMMENT '关联的 focus item',
  `is_enabled` BOOLEAN DEFAULT TRUE,
  `last_fired_at` BIGINT COMMENT '上次触发时间戳',
  `fire_count` INT DEFAULT 0,
  `max_fires` INT COMMENT '最大触发次数，NULL=无限',
  `cooldown_seconds` INT DEFAULT 60,
  `is_system` BOOLEAN DEFAULT FALSE COMMENT '系统触发器不可删除',
  `created_at` BIGINT NOT NULL,
  `expires_at` BIGINT COMMENT '过期时间戳',
  `updated_at` BIGINT NOT NULL,
  INDEX `idx_agent_enabled` (`agent_id`, `is_enabled`),
  UNIQUE KEY `uk_agent_name` (`agent_id`, `name`)
);
```

### 4.2 trigger_invocations 表 (执行队列)

```sql
CREATE TABLE IF NOT EXISTS `trigger_invocations` (
  `id` VARCHAR(36) PRIMARY KEY,
  `trigger_id` VARCHAR(36) NOT NULL,
  `agent_id` VARCHAR(64) NOT NULL,
  `status` ENUM('pending', 'claimed', 'completed', 'failed') DEFAULT 'pending',
  `claimed_at` BIGINT COMMENT '认领时间',
  `completed_at` BIGINT COMMENT '完成时间',
  `error` TEXT COMMENT '错误信息',
  `created_at` BIGINT NOT NULL,
  INDEX `idx_status` (`status`),
  INDEX `idx_agent_status` (`agent_id`, `status`)
);
```

### 4.3 plaza_posts 表 (可选，简化版 Plaza)

```sql
CREATE TABLE IF NOT EXISTS `plaza_posts` (
  `id` VARCHAR(36) PRIMARY KEY,
  `agent_id` VARCHAR(64) NOT NULL,
  `content` TEXT NOT NULL,
  `like_count` INT DEFAULT 0,
  `comment_count` INT DEFAULT 0,
  `created_at` BIGINT NOT NULL
);
```

---

## 5. API 设计

### 5.1 Trigger API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/agents/{agent_id}/triggers` | 列出触发器 |
| POST | `/api/agents/{agent_id}/triggers` | 创建触发器 |
| GET | `/api/agents/{agent_id}/triggers/{trigger_id}` | 获取触发器 |
| PUT | `/api/agents/{agent_id}/triggers/{trigger_id}` | 更新触发器 |
| DELETE | `/api/agents/{agent_id}/triggers/{trigger_id}` | 删除触发器 |

#### POST /api/agents/{agent_id}/triggers

请求：
```json
{
  "name": "daily-report",
  "type": "cron",
  "config": {"expr": "0 9 * * 1-5"},
  "reason": "每日早9点生成报告",
  "cooldown_seconds": 3600,
  "max_fires": null
}
```

响应：
```json
{
  "id": "uuid-xxx",
  "agent_id": "agent_1",
  "name": "daily-report",
  "type": "cron",
  "config": {"expr": "0 9 * * 1-5"},
  "reason": "每日早9点生成报告",
  "is_enabled": true,
  "fire_count": 0,
  "created_at": 1234567890
}
```

### 5.2 Plaza API (简化)

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/plaza/posts` | 获取最近帖子 |
| POST | `/api/plaza/posts` | 发帖 |
| POST | `/api/plaza/posts/{id}/comments` | 评论 |

---

## 6. 实施计划

### Phase 1: 数据库与基础设施

- [ ] 新增 `triggers` 表
- [ ] 新增 `trigger_invocations` 表
- [ ] Coordinator models.go 新增 Trigger/TriggerInvocation 模型
- [ ] Storage.go 新增 Trigger 存储方法

### Phase 2: Trigger API

- [ ] Coordinator handler.go 新增 Trigger CRUD handlers
- [ ] Coordinator main.go 注册 Trigger 路由

### Phase 3: Trigger Daemon

- [ ] Coordinator 新增 trigger_daemon.go
- [ ] Cron 表达式解析 (推荐使用 robfig/cron)
- [ ] Interval 评估
- [ ] Poll HTTP 评估
- [ ] Dedup 检查 (30s 窗口)
- [ ] invocation 入队

### Phase 4: x-client 集成

- [ ] x-client 新增 Trigger Worker
- [ ] Intent 路由新增 TRIGGER 类型
- [ ] handleTriggerMessage() 处理函数
- [ ] trigger_daemon 客户端 (polling invocation)

### Phase 5: HEARTBEAT

- [ ] HEARTBEAT.md 指令模板
- [ ] x-client heartbeat.go 服务
- [ ] Phase 1-4 执行逻辑
- [ ] Plaza API 调用 (plaza_get_new_posts, plaza_create_post, plaza_add_comment)
- [ ] curiosity_journal 写入
- [ ] active_hours 检测

### Phase 6: 测试

- [ ] Trigger API 单元测试
- [ ] Trigger Daemon 集成测试
- [ ] HEARTBEAT 流程测试
- [ ] 端到端测试

---

## 7. 关键文件变更

### 7.1 Coordinator HTTP

| 文件 | 变更 |
|------|------|
| `sql/schema.sql` | 新增 triggers, trigger_invocations 表 |
| `models.go` | 新增 Trigger, TriggerInvocation 模型 |
| `storage.go` | 新增 Trigger 存储方法 |
| `handler.go` | 新增 Trigger CRUD handlers |
| `main.go` | 注册 Trigger 路由，启动 Trigger Daemon |
| `trigger_daemon.go` | 新增，15s tick 循环 |

### 7.2 x-client HTTP

| 文件 | 变更 |
|------|------|
| `models.go` | PollMessage 新增 type=trigger，TriggerInfo |
| `main.go` | Intent 路由新增 TRIGGER，Heartbeat Service 启动 |
| `heartbeat.go` | 新增，HEARTBEAT 执行逻辑 |
| `memory.go` | 新增 curiosity_journal 写入 |
| `plaza.go` | 新增，Plaza API 调用 |

### 7.3 新增文件

```
x-client-http/
├── trigger_worker.go    # Trigger 处理
├── heartbeat.go         # HEARTBEAT 执行
├── plaza.go             # Plaza API
└── cron.go              # Cron 解析辅助
```

---

## 8. 配置文件

### 8.1 x-client-http config.json

```json
{
  "agent_id": "agent_1",
  "coordinator_url": "http://localhost:8080",
  "agentcore_url": "http://localhost:8000",
  "poll_interval_ms": 1000,
  "heartbeat": {
    "enabled": true,
    "interval_seconds": 60,
    "active_hours": "09:00-18:00"
  },
  "trigger": {
    "poll_interval_ms": 10000
  }
}
```

---

## 9. 集成点

### 9.1 与现有消息处理流程集成

```
Trigger 触发
    ↓
x-client 收到 intent=TRIGGER 的消息
    ↓
handleTriggerMessage()
    ├── 解析 trigger config
    ├── 生成任务描述
    └── 调用 AgentCore 处理
```

### 9.2 与 Task 系统集成

Trigger 可绑定 Focus Item：
```json
{
  "trigger": {
    "name": "daily-report",
    "focus_ref": "daily-report-001"
  }
}
```

Trigger 触发时自动创建关联 Task。

---

## 10. 参考

- Clawith Trigger 模型：`backend/app/models/trigger.py`
- Clawith Trigger Daemon：`backend/app/services/trigger_daemon.py`
- Clawith HEARTBEAT：`backend/app/services/heartbeat.py`
- Clawith HEARTBEAT 模板：`backend/agent_template/HEARTBEAT.md`

---

## 11. Agent 模板系统

> 本节描述如何为 x-client 引入标准化的 Agent 模板系统

### 11.1 现状分析

x-client 当前只有 `DelegateCommand` 解析，缺乏人格抽象：

```go
// 现状：只有功能性命令解析
type DelegateCommand struct {
    TaskID       string
    Title        string
    Description  string
    AssignedTo   string
    FocusItems   []string
    IsValid      bool
}
```

Clawith 的 Agent 模板包含完整的人格定义和对话流程，远超单纯的任务委托。

### 11.2 模板文件结构

```
{agent_id}/
├── soul.md           # Agent 人格定义
├── bootstrap.md      # 对话流程模板
└── meta.yaml         # 模板元数据
```

### 11.3 soul.md — Agent 人格定义

**用途**：定义 Agent 的身份、性格、工作方式和边界

```markdown
# Soul — {name}

## Identity
- **Role**: [角色名称]
- **Expertise**: [专业领域]
- **Creator**: [创建者]

## Personality
- [性格特点1]
- [性格特点2]

## Work Style
- [工作方式1]
- [工作方式2]

## Boundaries
- [行为边界1]
- [行为边界2]
```

**示例 (code-reviewer soul.md)**：

```markdown
# Soul — Code Reviewer

## Identity
- **Role**: Code Reviewer
- **Expertise**: Correctness review, security (OWASP top 10), concurrency issues

## Personality
- Direct but constructive — every comment explains the risk, not just the taste
- Skips bikeshedding (style, naming preferences) unless it threatens legibility
- Willing to say "looks good, ship it" without padding

## Work Style
- Start with the diff's intent before judging any line
- Classify findings into blocking / non-blocking / nit
- During heartbeat: focus on newly disclosed CVEs, language release notes

## Boundaries
- I review and recommend; merging the PR is always the user's decision
- I don't rewrite the PR inline
```

### 11.4 bootstrap.md — 对话流程模板

**用途**：定义 Agent 首次对话和后续对话的标准流程

```markdown
You are {name}, a [role] meeting {user_name} for the first time.

This conversation has had {user_turns} user messages so far.
Follow EXACTLY the matching branch below.

If user_turns == 0 (greeting turn):
- [首次见面的开场流程]
- [能力介绍]
- [引导下一步]

If user_turns >= 1 (deliverable turn):
- [交付物处理流程]
- [输出格式]
- [收尾引导]
```

**示例 (code-reviewer bootstrap.md)**：

```markdown
If user_turns == 0 (greeting turn):
- Open with: "**Hi {user_name}!**"
- One-line intro: "I'm **{name}** — direct code review, focused on what matters."
- Pitch 2–3 capability bullets:
  - "**Correctness & edge cases** — what breaks at month-end."
  - "**Security** — OWASP-level issues caught early."
- Ask ONE bolded question: "**Paste a diff, a file, or a function you want reviewed**."

If user_turns >= 1 (deliverable turn):
- Produce review with bold section headers:
  - "**What this change does**"
  - "**Blocking**" — issues must be fixed before merge
  - "**Non-blocking**" — concerns worth noting
  - "**Nits**" — optional polish (0-3 items)
- Close: "Want me to **dig deeper on the blocking items**?"
- Under ~500 words.
```

### 11.5 meta.yaml — 模板元数据

**用途**：定义模板的显示信息、能力列表、默认权限策略

```yaml
name: "Agent Name"
description: "One-line description of what this agent does."
icon: "AB"           # 2-letter icon for UI
category: "category"  # software-development / productivity / analysis / ...
capability_bullets:
  - "Capability 1 — brief description"
  - "Capability 2 — brief description"
default_skills: []
default_autonomy_policy:
  read_files: "L1"
  write_workspace_files: "L2"
  delete_files: "L2"
  send_feishu_message: "L2"
```

**示例**：

```yaml
name: "Backend Architect"
description: "Designs APIs, data models, and service boundaries that hold up."
icon: "BA"
category: "software-development"
capability_bullets:
  - "API design — REST/GraphQL shapes with clear contracts"
  - "Data modeling — schema, indexes, partitioning"
  - "Trade-off analysis — CAP, consistency, latency vs cost"
default_autonomy_policy:
  read_files: "L1"
  write_workspace_files: "L1"
  delete_files: "L2"
```

### 11.6 与现有 DelegateCommand 的关系

| 方面 | DelegateCommand | Agent Template |
|------|---------------|----------------|
| **定位** | 功能性任务委托 | 完整人格+对话流程 |
| **内容** | TaskID, Title, Focus | Identity, Personality, Work Style |
| **触发** | `/delegate` 命令 | 任意消息 + user_turns |
| **复用** | 每次新建 | 可持久化人格 |
| **适用** | 临时任务分配 | 长期角色扮演 |

**集成建议**：

```
DelegateCommand (现有)
    │
    ├── 解析 /delegate 命令
    │
    └── 加载 soul.md → 补充人格上下文
            │
            └── bootstrap.md → 确定对话流程
```

### 11.7 实施阶段

| 阶段 | 任务 | 工作量 |
|------|------|--------|
| **11.1** | 定义模板文件结构 (`soul.md`, `bootstrap.md`, `meta.yaml`) | 小 |
| **11.2** | 模板加载器 (`template.go`) | 中 |
| **11.3** | Bootstrap 对话引擎 (`bootstrap.go`) | 中 |
| **11.4** | Soul 上下文注入 | 小 |
| **11.5** | user_turns 计数器 | 小 |
| **11.6** | 模板注册 API (可选) | 中 |

### 11.8 关键文件变更

| 文件 | 变更 |
|------|------|
| `x-client-http/models.go` | 新增 `AgentTemplate`, `Soul`, `Bootstrap` 模型 |
| `x-client-http/template.go` | 新增，模板加载和解析 |
| `x-client-http/bootstrap.go` | 新增，对话流程引擎 |
| `x-client-http/main.go` | 集成模板上下文 |

### 11.9 配置示例

```json
{
  "agent_id": "agent_1",
  "template": {
    "soul_path": "./templates/agent_1/soul.md",
    "bootstrap_path": "./templates/agent_1/bootstrap.md",
    "meta_path": "./templates/agent_1/meta.yaml"
  }
}
```

**目录结构**：

```
templates/
└── agent_1/
    ├── soul.md
    ├── bootstrap.md
    └── meta.yaml
```
