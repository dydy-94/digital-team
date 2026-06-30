# X-Client Plugin for Claude Agent SDK

提供多 Agent 协作能力的 Tool 集合。

## 安装

```bash
cd plugins/python
pip install -e .
```

## 使用方法

### 基本使用

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

### 获取上下文示例

```python
# Agent 可以调用 list_room_agents 查看聊天室成员
result = await agent.call_tool("list_room_agents", {"room_id": "room-dev"})
# 返回: "聊天室 room-dev 的成员:
#        • agent-001 - UI设计师 [在线]
#        • agent-002 - 后端开发 [离线]"
```

## Available Tools

### 1. send_message_to_agent

向聊天室中的指定 Agent 发送消息并等待回复。

```python
await agent.call_tool("send_message_to_agent", {
    "target_agent": "agent-002",
    "message": "帮我完成登录页面",
    "room_id": "room-dev",
    "intent": "DELEGATE"
})
```

### 2. list_room_agents

查询聊天室成员。

```python
await agent.call_tool("list_room_agents", {
    "room_id": "room-dev"
})
```

### 3. get_agent_context

获取 Agent 详细信息。

```python
await agent.call_tool("get_agent_context", {
    "agent_id": "agent-002",
    "room_id": "room-dev"
})
```

### 4. create_task

创建任务。

```python
await agent.call_tool("create_task", {
    "title": "完成登录页面",
    "assigned_to": "agent-002",
    "room_id": "room-dev",
    "description": "设计和实现登录页面"
})
```

### 5. query_task

查询任务。

```python
await agent.call_tool("query_task", {
    "task_id": "task-xxx"
})
```

### 6. upload_file

上传文件到聊天室，其他成员可下载。

**流程**：先上传到 S3，获取 transfer_id，然后可通过消息发送。

```python
await agent.call_tool("upload_file", {
    "file_path": "/path/to/file.pdf",
    "file_name": "design.pdf",  # 可选
    "to_agent": "agent-002",     # 可选，指定接收者
    "room_id": "room-dev"
})
# 返回: "文件上传成功 ✅
#        文件名: design.pdf
#        大小: 102400 bytes
#        传输ID: abc123
#        其他成员可使用 transfer_id 下载此文件"
```

### 7. download_file

根据 transfer_id 下载文件到工作区。

```python
await agent.call_tool("download_file", {
    "transfer_id": "abc123",
    "save_path": "/custom/path/file.pdf"  # 可选
})
# 返回: "文件下载成功 ✅
#        传输ID: abc123
#        保存路径: /custom/path/file.pdf
#        大小: 102400 bytes"

## 文件传输流程

当 Agent 需要向其他 Agent 发送文件时，采用**先上传后引用**的模式：

```
1. upload_file() ──> S3 ──> transfer_id
2. send_message(transfer_id, intent=FILE) ──> 其他 Agent
3. 其他 Agent 自动下载: download_file(transfer_id)
```

**示例**：Agent A 向 Agent B 发送设计稿

```python
# Step 1: 上传文件
upload_result = await agent.call_tool("upload_file", {
    "file_path": "/workspace/design.pdf",
    "room_id": "room-dev"
})
# 获取 transfer_id: "abc123"

# Step 2: 发送文件引用
await agent.call_tool("send_message_to_agent", {
    "target_agent": "agent-002",
    "message": "请查看设计稿，transfer_id: abc123",
    "room_id": "room-dev",
    "intent": "FILE"  # 关键：intent=FILE
})
```

Agent B 收到消息后，X-Client 会自动检测 `intent=FILE`，并从 S3 下载文件到 `downloads/{room_id}/` 目录。

## 工作目录结构

Agent 在聊天室中工作时，有独立的工作目录。目录按 `{agent_id}/{room_id}` 维度隔离：

```
~/.x-client/workspace/{agent_id}/{room_id}/
├── inbox/
│   └── messages/           # 接收到的消息缓存
├── uploads/{room_id}/    # 用户上传给 Agent 的文件
├── downloads/{room_id}/  # Agent 下载的文件缓存
├── reports/{room_id}/    # AgentCore 产出的报告
└── [其他文件]            # Agent 工作过程中生成的文件
```

**各目录用途**：

| 目录 | 用途 |
|------|------|
| `inbox/messages` | 消息缓存，Agent 可读取历史消息 |
| `uploads/{room_id}` | 用户/其他 Agent 上传给当前 Agent 的文件 |
| `downloads/{room_id}` | Agent 从 S3 等下载的文件缓存 |
| `reports/{room_id}` | Agent 输出的报告文件 |

Agent 在推理时应按需读取对应目录下的文件，例如：
- 读取 `uploads/{room_id}/` 下的用户上传文件
- 将工作产物写入当前工作目录
- 将报告输出到 `reports/{room_id}/`
