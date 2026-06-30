# Agent 关系感知与协作系统设计方案

> 本文档描述如何通过 X-Client Plugin 实现 Agent 关系感知与自主协作能力

## 目录

1. [背景与目标](#1-背景与目标)
2. [整体架构](#2-整体架构)
3. [数据库设计](#3-数据库设计)
4. [Coordinator API 扩展](#4-coordinator-api-扩展)
5. [X-Client 增强](#5-x-client-增强)
6. [X-Client Plugin 设计](#6-x-client-plugin-设计)
7. [协作流程](#7-协作流程)

---

## 1. 背景与目标

### 1.1 问题

当前 x-client 项目虽然提供了消息路由、任务委托等基础能力，但 AgentCore 在推理时：
- **无法感知** 当前聊天室中的其他 Agent
- **无法了解** Agent 之间的关系（同事、上下级）
- **无法主动** 决定是否需要协作

### 1.2 目标

1. **关系感知**：让 Agent 在推理时能看到同事、上下级信息
2. **自主决策**：Agent 能自主决定何时需要协作
3. **低耦合**：通过 Plugin 方式集成，不修改 AgentCore 核心代码
4. **精确触发**：Agent 只有在需要时才会调用协作工具

---

## 2. 整体架构

### 2.1 架构图

```
┌─────────────────────────────────────────────────────────────────────┐
│                    AgentCore (Claude Agent SDK)                        │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │  Plugin 系统 (可插拔)                                          │     │
│  │  ┌─────────────────────────────────────────────────────┐   │     │
│  │  │  X-Client Plugin                                    │   │     │
│  │  │  • send_message_to_agent                           │   │     │
│  │  │  • list_room_agents                                │   │     │
│  │  │  • get_agent_context                               │   │     │
│  │  │  • create_task / query_task                        │   │     │
│  │  └─────────────────────────────────────────────────────┘   │     │
│  └─────────────────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────────────┘
                              │
                              │ Tool 调用
                              ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      X-Client HTTP                                   │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │  API 端点                                                    │     │
│  │  • GET  /api/room/{id}/agents     - 获取聊天室成员           │     │
│  │  • GET  /api/agent/context        - 获取 Agent 上下文       │     │
│  │  • POST /api/agent/relation       - 管理关系                 │     │
│  │  • POST /api/message              - 发送消息                 │     │
│  └─────────────────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────────┐
│                      Coordinator HTTP                                 │
│  ┌─────────────────────────────────────────────────────────────┐     │
│  │  Storage: MySQL                                            │     │
│  │  ┌───────────┐  ┌───────────┐  ┌───────────┐              │     │
│  │  │ Members   │  │ Relations │  │ RoomConfigs│              │     │
│  │  │ 成员管理   │  │ 关系管理   │  │ 聊天室配置  │              │     │
│  │  └───────────┘  └───────────┘  └───────────┘              │     │
│  └─────────────────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────────────┘
```

### 2.2 消息流转

```
Agent A                          X-Client A                        Coordinator
  │                                  │                                  │
  │  LLM 推理需要协作                  │                                  │
  │  Tool: send_message_to_agent      │                                  │
  │ ─────────────────────────────────►│                                  │
  │                                  │  POST /api/message               │
  │                                  │ ─────────────────────────────────►│
  │                                  │                                  │
  │                                  │                                  │ 存储消息
  │                                  │                                  │ 检测 @agent-B
  │                                  │                                  │
  │                                  │◄─────────────────────────────────│
  │                                  │  广播到聊天室                     │
  │                                  │                                  │
  │◄─────────────────────────────────│                                  │
  │  收到 Agent B 的回复               │                                  │
```

---

## 3. 数据库设计

### 3.1 新增表

#### agent_relations - Agent 关系表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT | 主键 |
| agent_id | VARCHAR(64) | Agent ID |
| relation_type | VARCHAR(20) | 关系类型：colleague/superior/subordinate |
| related_agent_id | VARCHAR(64) | 关联的 Agent ID |
| room_id | VARCHAR(64) | 关联的聊天室（可选）|
| description | TEXT | 关系描述 |
| created_at | DATETIME | 创建时间 |

**唯一索引：** `(agent_id, relation_type, related_agent_id)`

#### room_configs - 聊天室配置表

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | VARCHAR(64) | 聊天室 ID (PK) |
| config | JSON | 聊天室配置 |
| created_at | DATETIME | 创建时间 |
| updated_at | DATETIME | 更新时间 |

**config 示例：**
```json
{
  "name": "开发团队",
  "hierarchy_enabled": true,
  "auto_welcome": true,
  "welcome_message": "欢迎来到开发团队聊天室..."
}
```

#### agents 表扩展

| 字段 | 类型 | 说明 |
|------|------|------|
| role | VARCHAR(100) | Agent 角色，如"设计师"、"开发"、"测试" |
| description | TEXT | Agent 描述 |
| avatar | VARCHAR(255) | 头像 URL |

### 3.2 ER 关系图

```
┌─────────────┐       ┌──────────────────┐       ┌─────────────┐
│   agents    │       │ agent_relations  │       │    rooms    │
├─────────────┤       ├──────────────────┤       ├─────────────┤
│ agent_id (PK)│◄──────│ agent_id (FK)    │       │ room_id (PK)│
│ role         │       │ relation_type    │       │ name        │
│ description  │       │ related_agent_id │──────►│             │
│ avatar       │       │ room_id (FK)     │◄──────│             │
└─────────────┘       │ description      │       └─────────────┘
                      └──────────────────┘
                               │
                               │ 1:N (一个 Agent 有多条关系)
                               ▼
                      ┌──────────────────┐
                      │  agent_relations │
                      ├──────────────────┤
                      │ relation_type:    │
                      │   • colleague    │
                      │   • superior     │
                      │   • subordinate  │
                      └──────────────────┘
```

---

## 4. Coordinator API 扩展

### 4.1 Agent 关系 API

#### 创建/更新关系

```
POST /api/agent/relation
```

**请求体：**
```json
{
  "agent_id": "agent-002",
  "relation_type": "colleague",
  "related_agent_id": "agent-003",
  "room_id": "room-dev",
  "description": "测试搭档"
}
```

**响应：**
```json
{
  "success": true,
  "relation_id": 1
}
```

#### 删除关系

```
DELETE /api/agent/relation/{relation_id}
```

#### 获取 Agent 关系

```
GET /api/agent/{agent_id}/relations?room_id={room_id}
```

**响应：**
```json
{
  "success": true,
  "relations": [
    {
      "id": 1,
      "agent_id": "agent-002",
      "relation_type": "colleague",
      "related_agent_id": "agent-003",
      "description": "测试搭档"
    },
    {
      "id": 2,
      "agent_id": "agent-002",
      "relation_type": "subordinate",
      "related_agent_id": "agent-004",
      "description": "运维下属"
    }
  ]
}
```

### 4.2 Agent 上下文 API

#### 获取 Agent 完整上下文

```
GET /api/agent/context?agent_id={agent_id}&room_id={room_id}
```

**响应：**
```json
{
  "success": true,
  "context": {
    "current_agent": {
      "agent_id": "agent-002",
      "role": "后端开发",
      "description": "负责后端开发"
    },
    "room_members": [
      {
        "agent_id": "agent-001",
        "role": "UI设计师",
        "online": true
      },
      {
        "agent_id": "agent-002",
        "role": "后端开发",
        "online": true
      },
      {
        "agent_id": "agent-003",
        "role": "测试工程师",
        "online": false
      }
    ],
    "relations": {
      "colleagues": ["agent-001", "agent-003"],
      "superiors": ["agent-001"],
      "subordinates": ["agent-004"]
    },
    "room_config": {
      "name": "开发团队",
      "hierarchy_enabled": true
    }
  }
}
```

### 4.3 聊天室 Agent API

#### 获取聊天室成员

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
      "role": "UI设计师",
      "online": true,
      "relations": {
        "colleagues": ["agent-002", "agent-003"],
        "superiors": [],
        "subordinates": ["agent-002"]
      }
    }
  ]
}
```

### 4.4 聊天室配置 API

#### 创建/更新聊天室配置

```
PUT /api/room/{room_id}/config
```

**请求体：**
```json
{
  "name": "开发团队",
  "hierarchy_enabled": true,
  "auto_welcome": true,
  "welcome_message": "欢迎来到开发团队！\n\n可用命令：\n• @agent-xxx - 向同事发消息"
}
```

---

## 5. X-Client 增强

### 5.1 新增 API 调用

```go
// 获取聊天室 Agent 列表
func (x *XClient) GetRoomAgents(roomID string) ([]AgentInfo, error)

// 获取 Agent 上下文
func (x *XClient) GetAgentContext(roomID, agentID string) (*AgentContext, error)

// 获取 Agent 关系
func (x *XClient) GetAgentRelations(agentID, roomID string) ([]Relation, error)
```

### 5.2 AgentContext 结构

```go
type AgentContext struct {
    CurrentAgent  *AgentInfo    `json:"current_agent"`
    RoomMembers   []AgentInfo   `json:"room_members"`
    Relations     *Relations    `json:"relations"`
    RoomConfig    *RoomConfig   `json:"room_config"`
}

type Relations struct {
    Colleagues    []string `json:"colleagues"`
    Superiors     []string `json:"superiors"`
    Subordinates  []string `json:"subordinates"`
}
```

---

## 6. X-Client Plugin 设计

### 6.1 Plugin 概述

```
X-Client Plugin
├── 名称: x_client_collaboration
├── 描述: 多 Agent 协作工具集
└── Tools:
    ├── send_message_to_agent  - 向 Agent 发送消息
    ├── list_room_agents       - 获取聊天室成员
    ├── get_agent_context      - 获取 Agent 上下文
    ├── create_task            - 创建任务
    └── query_task             - 查询任务
```

### 6.2 Tool 定义

#### send_message_to_agent

```python
Tool(
    name="send_message_to_agent",
    description="""向聊天室中的指定 Agent 发送消息并等待回复。

使用场景：
- 需要其他 Agent 的帮助或专业意见
- 需要委托任务给其他 Agent
- 需要询问其他 Agent 相关信息

注意：消息会自动 @ 目标 Agent，目标 Agent 会收到通知并回复。""",
    input_schema={
        "type": "object",
        "properties": {
            "target_agent": {"type": "string", "description": "目标 Agent ID"},
            "message": {"type": "string", "description": "消息内容"},
            "room_id": {"type": "string", "description": "聊天室 ID"},
            "intent": {"type": "string", "enum": ["DELEGATE", "INFORM", "QUERY"]}
        },
        "required": ["target_agent", "message", "room_id"]
    }
)
```

#### list_room_agents

```python
Tool(
    name="list_room_agents",
    description="查询当前聊天室的所有 Agent 成员及其角色、关系",
    input_schema={
        "type": "object",
        "properties": {
            "room_id": {"type": "string", "description": "聊天室 ID"}
        },
        "required": ["room_id"]
    }
)
```

#### get_agent_context

```python
Tool(
    name="get_agent_context",
    description="获取指定 Agent 的详细信息，包括同事关系、上下级关系",
    input_schema={
        "type": "object",
        "properties": {
            "agent_id": {"type": "string", "description": "Agent ID"},
            "room_id": {"type": "string", "description": "聊天室 ID"}
        },
        "required": ["agent_id", "room_id"]
    }
)
```

### 6.3 Plugin 使用示例

```python
from x_client_plugin import XClientPlugin
from claude_agent_sdk import Agent

# 创建 Plugin
plugin = XClientPlugin(x_client_url="http://localhost:8081")

# 创建 Agent 并加载 Plugin
agent = Agent(name="agent-001", model="claude-sonnet-4")
agent.load_plugin(plugin)

# 启动
agent.run()
```

---

## 7. 协作流程

### 7.1 Agent 自主协作流程

```
1. AgentCore 收到任务
       │
       ▼
2. LLM 推理，判断需要协作
       │
       ▼
3. Agent 调用 Tool: list_room_agents
       │
       ▼
4. 查看同事列表，选择合适的 Agent
       │
       ▼
5. Agent 调用 Tool: send_message_to_agent
       │
       ▼
6. X-Client Plugin → X-Client → Coordinator
       │
       ▼
7. Coordinator 路由消息到目标 Agent
       │
       ▼
8. 目标 Agent 收到消息，处理
       │
       ▼
9. 目标 Agent 返回结果
       │
       ▼
10. 发起方 Agent 收到回复，LLM 继续推理
```

### 7.2 完整消息流

```
Agent A                          X-Client A                        Coordinator                        Agent B
  │                                  │                                  │                                  │
  │  Tool: list_room_agents         │                                  │                                  │
  │ ─────────────────────────────────►│                                  │                                  │
  │                                  │  GET /api/room/xxx/agents       │                                  │
  │                                  │ ─────────────────────────────────►│                                  │
  │                                  │                                  │                                  │
  │                                  │◄─────────────────────────────────│                                  │
  │                                  │  返回成员列表                     │                                  │
  │                                  │                                  │                                  │
  │◄─────────────────────────────────│                                  │                                  │
  │  "聊天室成员: agent-001(设计师), agent-002(开发)..."               │                                  │
  |                                  |                                  |                                  |
  |  Tool: send_message_to_agent     |                                  |                                  |
  |  Args: target=agent-002          |                                  |                                  |
  | ─────────────────────────────────►│                                  |                                  |
  |                                  |  POST /api/message               |                                  |
  |                                  | ─────────────────────────────────►│                                  |
  |                                  |                                  | 存储消息，检测 @agent-002         |
  |                                  |                                  |                                  |
  |                                  |                                  |  POST /skill/delegate            |
  |                                  |                                  | ───────────────────────────────►│
  |                                  |                                  |                                  |
  |                                  |                                  |                                  │ 唤醒 AgentCore B
  |                                  |                                  |                                  |
  |                                  |                                  |                                  │ 处理任务
  |                                  |                                  |                                  |
  |                                  |                                  |                                  │ 返回结果
  |                                  |                                  |                                  │
  |                                  |◄─────────────────────────────────│                                  │
  |                                  │  sendReply()                     │                                  │
  |                                  │                                  │                                  │
  |◄─────────────────────────────────│                                  │                                  │
  |  "任务已完成：登录页面已设计..."    |                                  |                                  │
```

### 7.3 LLM 决策示例

```
用户: 帮我完成这个项目

Agent A (LLM 推理):
  "这个任务涉及 UI 设计、后端开发、测试。
   我应该先调用 list_room_agents 了解团队成员，
   然后分别向 UI 设计师、后端开发、测试工程师发送任务。"

  ↓

Tool: list_room_agents
  返回: agent-001(UI设计师), agent-002(后端开发), agent-003(测试)

  ↓

Tool: send_message_to_agent
  Args: target=agent-001, message="帮我设计登录页面 UI", intent=DELEGATE

  ↓

Tool: send_message_to_agent
  Args: target=agent-002, message="帮我开发登录后端", intent=DELEGATE

  ↓

Tool: send_message_to_agent
  Args: target=agent-003, message="帮我测试登录功能", intent=DELEGATE
```

---

## 附录

### A. 关系类型说明

| 类型 | 说明 | 示例 |
|------|------|------|
| colleague | 同事关系 | 同级协作 |
| superior | 上级关系 | 汇报、审批 |
| subordinate | 下级关系 | 分配任务 |

### B. Intent 类型说明

| 类型 | 说明 | 行为 |
|------|------|------|
| DELEGATE | 委托任务 | 等待对方处理并返回结果 |
| INFORM | 通知 | 发送后不等待回复 |
| QUERY | 询问 | 等待对方回答问题 |

### C. 实施优先级

| 优先级 | 模块 | 原因 |
|--------|------|------|
| P0 | 数据库表 | 基础数据存储 |
| P0 | Coordinator API | 提供数据接口 |
| P1 | X-Client API | Plugin 调用 |
| P1 | X-Client Plugin | Agent 集成 |
| P2 | 聊天室欢迎消息 | 用户体验 |
| P2 | 关系管理界面 | 方便配置 |
