# Agent 关系感知与协作系统 - 实施计划

> 本文档是 [agent-collaboration-design.md](./agent-collaboration-design.md) 的实施指南

## 实施阶段

| 阶段 | 内容 | 优先级 |
|------|------|--------|
| Phase 0 | 基础设施准备 | P0 |
| Phase 1 | 数据库设计 | P0 |
| Phase 2 | Coordinator API 扩展 | P0 |
| Phase 3 | X-Client API 增强 | P1 |
| Phase 4 | X-Client Plugin 开发 | P1 |
| Phase 5 | 测试与集成 | P1 |
| Phase 6 | 高级功能 | P2 |

---

## Phase 0: 基础设施准备

### 0.1 确认环境

- [ ] Go 1.21+
- [ ] MySQL 8.0+
- [ ] Claude Agent SDK (Python)

### 0.2 创建项目目录

```bash
mkdir -p /path/to/x-client/plugins/python
```

---

## Phase 1: 数据库设计

### 1.1 新增 SQL 表

创建文件 `sql/agent_relations.sql`：

```sql
-- =====================================================
-- Agent 关系管理表
-- =====================================================

-- Agent 关系表
CREATE TABLE IF NOT EXISTS agent_relations (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    agent_id VARCHAR(64) NOT NULL COMMENT 'Agent ID',
    relation_type VARCHAR(20) NOT NULL COMMENT '关系类型: colleague/superior/subordinate',
    related_agent_id VARCHAR(64) NOT NULL COMMENT '关联的 Agent ID',
    room_id VARCHAR(64) COMMENT '关联的聊天室（可选）',
    description TEXT COMMENT '关系描述',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    
    -- 确保同一 Agent 的同一关系类型不会有重复关联
    UNIQUE KEY uk_agent_relation (agent_id, relation_type, related_agent_id),
    
    -- 索引
    INDEX idx_agent_id (agent_id),
    INDEX idx_room_id (room_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Agent 关系表';

-- =====================================================
-- 聊天室配置表
-- =====================================================

CREATE TABLE IF NOT EXISTS room_configs (
    room_id VARCHAR(64) PRIMARY KEY COMMENT '聊天室 ID',
    config JSON COMMENT '聊天室配置',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='聊天室配置表';

-- =====================================================
-- agents 表扩展字段（如果需要）
-- =====================================================

ALTER TABLE agents 
ADD COLUMN IF NOT EXISTS role VARCHAR(100) COMMENT 'Agent 角色' AFTER endpoint,
ADD COLUMN IF NOT EXISTS description TEXT COMMENT 'Agent 描述' AFTER role,
ADD COLUMN IF NOT EXISTS avatar VARCHAR(255) COMMENT '头像 URL' AFTER description;
```

### 1.2 执行 SQL

```bash
mysql -u root -p xclient < sql/agent_relations.sql
```

### 1.3 验证

```sql
SHOW TABLES LIKE 'agent_%';
-- 应该看到: agent_relations

DESCRIBE agent_relations;
```

---

## Phase 2: Coordinator API 扩展

### 2.1 修改 models.go

添加新的数据模型：

```go
// models.go

// RelationType 关系类型
const (
    RelationColleague   = "colleague"
    RelationSuperior    = "superior"
    RelationSubordinate  = "subordinate"
)

// AgentRelation Agent 关系
type AgentRelation struct {
    ID               int64   `json:"id"`
    AgentID          string  `json:"agent_id"`
    RelationType     string  `json:"relation_type"`
    RelatedAgentID   string  `json:"related_agent_id"`
    RoomID           string  `json:"room_id,omitempty"`
    Description      string  `json:"description,omitempty"`
    CreatedAt        int64   `json:"created_at"`
}

// RoomConfig 聊天室配置
type RoomConfig struct {
    RoomID            string  `json:"room_id"`
    Name              string  `json:"name"`
    HierarchyEnabled  bool    `json:"hierarchy_enabled"`
    AutoWelcome       bool    `json:"auto_welcome"`
    WelcomeMessage    string  `json:"welcome_message"`
}

// RelationRequest 创建/更新关系请求
type RelationRequest struct {
    AgentID         string `json:"agent_id"`
    RelationType    string `json:"relation_type"`
    RelatedAgentID  string `json:"related_agent_id"`
    RoomID          string `json:"room_id,omitempty"`
    Description     string `json:"description,omitempty"`
}

// AgentContext Agent 上下文
type AgentContext struct {
    CurrentAgent  *AgentInfo   `json:"current_agent"`
    RoomMembers   []AgentInfo `json:"room_members"`
    Relations     *Relations   `json:"relations"`
    RoomConfig    *RoomConfig  `json:"room_config"`
}

// Relations Agent 关系汇总
type Relations struct {
    Colleagues   []string `json:"colleagues"`
    Superiors    []string `json:"superiors"`
    Subordinates []string `json:"subordinates"`
}
```

### 2.2 修改 storage.go

添加关系相关的存储方法：

```go
// storage.go

// CreateRelation 创建关系
func (s *Storage) CreateRelation(rel *AgentRelation) (int64, error) {
    result, err := s.db.Exec(`
        INSERT INTO agent_relations (agent_id, relation_type, related_agent_id, room_id, description)
        VALUES (?, ?, ?, ?, ?)
        ON DUPLICATE KEY UPDATE description = VALUES(description)
    `, rel.AgentID, rel.RelationType, rel.RelatedAgentID, rel.RoomID, rel.Description)
    
    if err != nil {
        return 0, err
    }
    
    id, _ := result.LastInsertId()
    return id, nil
}

// GetAgentRelations 获取 Agent 的所有关系
func (s *Storage) GetAgentRelations(agentID string, roomID string) ([]AgentRelation, error) {
    var relations []AgentRelation
    query := `SELECT id, agent_id, relation_type, related_agent_id, room_id, description, created_at
              FROM agent_relations WHERE agent_id = ?`
    args := []interface{}{agentID}
    
    if roomID != "" {
        query += ` AND (room_id = ? OR room_id IS NULL)`
        args = append(args, roomID)
    }
    
    rows, err := s.db.Query(query, args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    
    for rows.Next() {
        var rel AgentRelation
        rows.Scan(&rel.ID, &rel.AgentID, &rel.RelationType, &rel.RelatedAgentID, &rel.RoomID, &rel.Description, &rel.CreatedAt)
        relations = append(relations, rel)
    }
    
    return relations, nil
}

// DeleteRelation 删除关系
func (s *Storage) DeleteRelation(relationID int64) error {
    _, err := s.db.Exec("DELETE FROM agent_relations WHERE id = ?", relationID)
    return err
}

// GetRoomConfig 获取聊天室配置
func (s *Storage) GetRoomConfig(roomID string) (*RoomConfig, error) {
    var configJSON string
    err := s.db.QueryRow("SELECT config FROM room_configs WHERE room_id = ?", roomID).Scan(&configJSON)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }
    
    var config RoomConfig
    json.Unmarshal([]byte(configJSON), &config)
    return &config, nil
}

// UpsertRoomConfig 创建/更新聊天室配置
func (s *Storage) UpsertRoomConfig(config *RoomConfig) error {
    configJSON, _ := json.Marshal(config)
    _, err := s.db.Exec(`
        INSERT INTO room_configs (room_id, config) VALUES (?, ?)
        ON DUPLICATE KEY UPDATE config = VALUES(config)
    `, config.RoomID, configJSON)
    return err
}
```

### 2.3 修改 handler.go

添加新的 API Handler：

```go
// handler.go

// RelationHandlers 关系管理 API

func (h *Handler) CreateRelationHandler(w http.ResponseWriter, r *http.Request) {
    var req RelationRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        h.writeError(w, http.StatusBadRequest, "无效的请求体")
        return
    }
    
    // 验证关系类型
    validTypes := map[string]bool{"colleague": true, "superior": true, "subordinate": true}
    if !validTypes[req.RelationType] {
        h.writeError(w, http.StatusBadRequest, "无效的关系类型")
        return
    }
    
    rel := &AgentRelation{
        AgentID:        req.AgentID,
        RelationType:    req.RelationType,
        RelatedAgentID:  req.RelatedAgentID,
        RoomID:         req.RoomID,
        Description:    req.Description,
    }
    
    id, err := h.storage.CreateRelation(rel)
    if err != nil {
        h.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    
    h.writeJSON(w, http.StatusOK, map[string]interface{}{
        "success": true,
        "relation_id": id,
    })
}

func (h *Handler) GetAgentRelationsHandler(w http.ResponseWriter, r *http.Request) {
    agentID := r.URL.Query().Get("agent_id")
    roomID := r.URL.Query().Get("room_id")
    
    relations, err := h.storage.GetAgentRelations(agentID, roomID)
    if err != nil {
        h.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    
    h.writeJSON(w, http.StatusOK, map[string]interface{}{
        "success": true,
        "relations": relations,
    })
}

func (h *Handler) DeleteRelationHandler(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    relationID, _ := strconv.ParseInt(vars["id"], 10, 64)
    
    if err := h.storage.DeleteRelation(relationID); err != nil {
        h.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    
    h.writeJSON(w, http.StatusOK, map[string]interface{}{
        "success": true,
    })
}

func (h *Handler) GetAgentContextHandler(w http.ResponseWriter, r *http.Request) {
    agentID := r.URL.Query().Get("agent_id")
    roomID := r.URL.Query().Get("room_id")
    
    // 获取 Agent 信息
    agent, _ := h.storage.GetAgent(agentID)
    
    // 获取聊天室成员
    members, _ := h.storage.GetRoomMembers(roomID)
    
    // 获取关系
    relations, _ := h.storage.GetAgentRelations(agentID, roomID)
    
    // 构建关系汇总
    relSummary := &Relations{}
    for _, rel := range relations {
        switch rel.RelationType {
        case "colleague":
            relSummary.Colleagues = append(relSummary.Colleagues, rel.RelatedAgentID)
        case "superior":
            relSummary.Superiors = append(relSummary.Superiors, rel.RelatedAgentID)
        case "subordinate":
            relSummary.Subordinates = append(relSummary.Subordinates, rel.RelatedAgentID)
        }
    }
    
    // 获取聊天室配置
    roomConfig, _ := h.storage.GetRoomConfig(roomID)
    
    context := &AgentContext{
        CurrentAgent: agent,
        RoomMembers:  members,
        Relations:    relSummary,
        RoomConfig:   roomConfig,
    }
    
    h.writeJSON(w, http.StatusOK, map[string]interface{}{
        "success": true,
        "context": context,
    })
}

func (h *Handler) GetRoomAgentsHandler(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    roomID := vars["room_id"]
    
    members, _ := h.storage.GetRoomMembers(roomID)
    
    // 为每个成员添加关系信息
    for i, member := range members {
        relations, _ := h.storage.GetAgentRelations(member.AgentID, roomID)
        relSummary := &Relations{}
        for _, rel := range relations {
            switch rel.RelationType {
            case "colleague":
                relSummary.Colleagues = append(relSummary.Colleagues, rel.RelatedAgentID)
            case "superior":
                relSummary.Superiors = append(relSummary.Superiors, rel.RelatedAgentID)
            case "subordinate":
                relSummary.Subordinates = append(relSummary.Subordinates, rel.RelatedAgentID)
            }
        }
        members[i].Relations = relSummary
    }
    
    h.writeJSON(w, http.StatusOK, map[string]interface{}{
        "success": true,
        "agents": members,
    })
}

func (h *Handler) UpdateRoomConfigHandler(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    roomID := vars["room_id"]
    
    var config RoomConfig
    if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
        h.writeError(w, http.StatusBadRequest, "无效的请求体")
        return
    }
    
    config.RoomID = roomID
    if err := h.storage.UpsertRoomConfig(&config); err != nil {
        h.writeError(w, http.StatusInternalServerError, err.Error())
        return
    }
    
    h.writeJSON(w, http.StatusOK, map[string]interface{}{
        "success": true,
    })
}
```

### 2.4 注册路由

在 main.go 中注册新路由：

```go
// main.go

func setupRoutes(r *mux.Router, h *Handler) {
    // ... 现有路由 ...
    
    // Agent 关系管理
    r.HandleFunc("/api/agent/relation", h.CreateRelationHandler).Methods("POST")
    r.HandleFunc("/api/agent/relations", h.GetAgentRelationsHandler).Methods("GET")
    r.HandleFunc("/api/agent/relation/{id}", h.DeleteRelationHandler).Methods("DELETE")
    r.HandleFunc("/api/agent/context", h.GetAgentContextHandler).Methods("GET")
    
    // 聊天室成员
    r.HandleFunc("/api/room/{room_id}/agents", h.GetRoomAgentsHandler).Methods("GET")
    
    // 聊天室配置
    r.HandleFunc("/api/room/{room_id}/config", h.UpdateRoomConfigHandler).Methods("PUT")
}
```

### 2.5 编译验证

```bash
cd coordinator-http
go build -o coordinator .
```

---

## Phase 3: X-Client API 增强

### 3.1 新增 API 调用方法

在 x-client-http 中添加新方法：

```go
// main.go

// GetRoomAgents 获取聊天室成员
func (x *XClient) GetRoomAgents(roomID string) ([]AgentInfo, error) {
    url := fmt.Sprintf("%s/api/room/%s/agents", x.coordinatorURL, roomID)
    
    resp, err := x.httpClient.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result struct {
        Success bool        `json:"success"`
        Agents  []AgentInfo `json:"agents"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }
    
    return result.Agents, nil
}

// GetAgentContext 获取 Agent 上下文
func (x *XClient) GetAgentContext(roomID, agentID string) (*AgentContext, error) {
    url := fmt.Sprintf("%s/api/agent/context?agent_id=%s&room_id=%s", 
        x.coordinatorURL, agentID, roomID)
    
    resp, err := x.httpClient.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result struct {
        Success bool          `json:"success"`
        Context *AgentContext `json:"context"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }
    
    return result.Context, nil
}

// GetAgentRelations 获取 Agent 关系
func (x *XClient) GetAgentRelations(agentID, roomID string) ([]AgentRelation, error) {
    url := fmt.Sprintf("%s/api/agent/relations?agent_id=%s&room_id=%s",
        x.coordinatorURL, agentID, roomID)
    
    resp, err := x.httpClient.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result struct {
        Success   bool            `json:"success"`
        Relations []AgentRelation `json:"relations"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }
    
    return result.Relations, nil
}
```

### 3.2 添加数据模型

```go
// models.go

// AgentRelation Agent 关系
type AgentRelation struct {
    ID             int64   `json:"id"`
    AgentID        string  `json:"agent_id"`
    RelationType   string  `json:"relation_type"`
    RelatedAgentID string  `json:"related_agent_id"`
    RoomID         string  `json:"room_id"`
    Description    string  `json:"description"`
}

// Relations Agent 关系汇总
type Relations struct {
    Colleagues   []string `json:"colleagues"`
    Superiors    []string `json:"superiors"`
    Subordinates []string `json:"subordinates"`
}

// AgentContext Agent 上下文
type AgentContext struct {
    CurrentAgent *AgentInfo  `json:"current_agent"`
    RoomMembers  []AgentInfo `json:"room_members"`
    Relations    *Relations  `json:"relations"`
    RoomConfig   *RoomConfig `json:"room_config"`
}

// RoomConfig 聊天室配置
type RoomConfig struct {
    RoomID           string `json:"room_id"`
    Name             string `json:"name"`
    HierarchyEnabled bool   `json:"hierarchy_enabled"`
    AutoWelcome      bool   `json:"auto_welcome"`
    WelcomeMessage   string `json:"welcome_message"`
}
```

### 3.3 编译验证

```bash
cd x-client-http
go build -o x-client .
```

---

## Phase 4: X-Client Plugin 开发

### 4.1 创建 Plugin 目录

```bash
mkdir -p /path/to/x-client/plugins/python/x_client_plugin
```

### 4.2 Plugin 实现

创建 `x_client_plugin/__init__.py`:

```python
"""
X-Client Plugin for Claude Agent SDK
提供多 Agent 协作能力的 Tool 集合
"""

from .plugin import XClientPlugin
from .tools import *

__all__ = ["XClientPlugin"]
```

创建 `x_client_plugin/plugin.py`:

```python
"""X-Client Plugin 主类"""

import httpx
from typing import List, Optional, Dict, Any
from claude_agent_sdk import BasePlugin, Tool, ToolResult


class XClientPlugin(BasePlugin):
    """X-Client 协作插件"""
    
    name = "x_client_collaboration"
    description = "多 Agent 协作工具集，支持消息发送、任务管理、Agent 查询等"
    
    def __init__(self, x_client_url: str):
        """
        初始化 X-Client Plugin
        
        Args:
            x_client_url: X-Client HTTP 服务地址，例如 "http://localhost:8081"
        """
        self.x_client_url = x_client_url.rstrip("/")
        self._tools = self._register_tools()
    
    def _register_tools(self) -> List[Tool]:
        """注册所有 Tools"""
        return [
            self._create_send_message_tool(),
            self._create_list_agents_tool(),
            self._create_get_context_tool(),
            self._create_create_task_tool(),
            self._create_query_task_tool(),
        ]
    
    def _create_send_message_tool(self) -> Tool:
        return Tool(
            name="send_message_to_agent",
            description=self._get_send_message_desc(),
            input_schema=self._get_send_message_schema(),
            handler=self.send_message_to_agent
        )
    
    def _create_list_agents_tool(self) -> Tool:
        return Tool(
            name="list_room_agents",
            description="查询当前聊天室的所有 Agent 成员及其角色、关系",
            input_schema={
                "type": "object",
                "properties": {
                    "room_id": {"type": "string", "description": "聊天室 ID"}
                },
                "required": ["room_id"]
            },
            handler=self.list_room_agents
        )
    
    # ... 其他 Tool 创建方法 ...
    
    # ============ Tool Handlers ============
    
    async def send_message_to_agent(self, **kwargs) -> ToolResult:
        """向 Agent 发送消息"""
        # 实现见设计文档
        pass
    
    async def list_room_agents(self, **kwargs) -> ToolResult:
        """获取聊天室成员"""
        # 实现见设计文档
        pass
    
    # ... 其他 Handler ...
```

创建 `x_client_plugin/tools.py`:

```python
"""Tool 具体实现"""

import httpx
from typing import Dict, Any
from .plugin import XClientPlugin


async def send_message_to_agent_impl(plugin: XClientPlugin, **kwargs) -> str:
    """send_message_to_agent 实现"""
    target_agent = kwargs["target_agent"]
    message = kwargs["message"]
    room_id = kwargs["room_id"]
    intent = kwargs.get("intent", "DELEGATE")
    
    async with httpx.AsyncClient(timeout=30.0) as client:
        response = await client.post(
            f"{plugin.x_client_url}/api/send",
            json={
                "room_id": room_id,
                "content": f"@{target_agent} {message}",
                "mention_users": [target_agent],
                "intent": intent
            }
        )
        
        if response.status_code == 200:
            return f"消息已发送给 {target_agent}，请等待回复..."
        else:
            return f"发送失败: {response.text}"


async def list_room_agents_impl(plugin: XClientPlugin, **kwargs) -> str:
    """list_room_agents 实现"""
    room_id = kwargs["room_id"]
    
    async with httpx.AsyncClient(timeout=10.0) as client:
        response = await client.get(
            f"{plugin.x_client_url}/api/room/{room_id}/agents"
        )
        
        if response.status_code == 200:
            data = response.json()
            agents = data.get("agents", [])
            
            output = f"聊天室 {room_id} 的成员:\n\n"
            for agent in agents:
                role = agent.get("role", "成员")
                status = "在线" if agent.get("online") else "离线"
                output += f"• {agent['agent_id']} - {role} ({status})\n"
            return output
        else:
            return "查询失败"


# ... 其他工具实现 ...
```

### 4.3 创建 setup.py

```python
# setup.py
from setuptools import setup, find_packages

setup(
    name="x-client-plugin",
    version="0.1.0",
    description="X-Client Plugin for Claude Agent SDK",
    packages=find_packages(),
    python_requires=">=3.8",
    install_requires=[
        "httpx>=0.24.0",
    ],
    extras_require={
        "sdk": ["claude-agent-sdk>=0.1.0"],
    },
)
```

### 4.4 安装测试

```bash
cd /path/to/x-client/plugins/python
pip install -e .
```

---

## Phase 5: 测试与集成

### 5.1 Coordinator API 测试

```bash
# 创建关系
curl -X POST "http://localhost:8080/api/agent/relation" \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "agent-002",
    "relation_type": "colleague",
    "related_agent_id": "agent-003",
    "room_id": "room-dev"
  }'

# 获取 Agent 关系
curl "http://localhost:8080/api/agent/relations?agent_id=agent-002&room_id=room-dev"

# 获取 Agent 上下文
curl "http://localhost:8080/api/agent/context?agent_id=agent-002&room_id=room-dev"

# 获取聊天室成员
curl "http://localhost:8080/api/room/room-dev/agents"
```

### 5.2 X-Client Plugin 测试

```python
# test_plugin.py
import asyncio
from x_client_plugin import XClientPlugin

async def test():
    plugin = XClientPlugin("http://localhost:8081")
    
    # 测试 list_room_agents
    result = await plugin.tools[1].handler(
        room_id="room-dev"
    )
    print(result.content)

asyncio.run(test())
```

---

## Phase 6: 高级功能 (P2)

### 6.1 聊天室欢迎消息

在 Coordinator 中添加自动发送欢迎消息的逻辑：

```go
func (h *Handler) CreateRoomHandler(w http.ResponseWriter, r *http.Request) {
    // ... 创建聊天室 ...
    
    // 检查是否需要发送欢迎消息
    config, _ := h.storage.GetRoomConfig(roomID)
    if config != nil && config.AutoWelcome {
        h.sendWelcomeMessage(roomID, config.WelcomeMessage)
    }
}

func (h *Handler) sendWelcomeMessage(roomID, message string) {
    // 发送系统欢迎消息
}
```

### 6.2 关系管理界面

可选：开发一个简单的 Web 界面来管理 Agent 关系。

---

## 验收标准

| 阶段 | 验收条件 |
|------|----------|
| Phase 1 | SQL 表创建成功，可以增删改查关系 |
| Phase 2 | API 可以创建/查询/删除 Agent 关系 |
| Phase 3 | X-Client 可以调用新 API |
| Phase 4 | Plugin 加载成功，Tools 可以调用 |
| Phase 5 | 完整流程测试通过 |

---

## 注意事项

1. **兼容性**：新增字段需要考虑向后兼容
2. **安全性**：API 需要添加认证（后续）
3. **性能**：关系查询需要添加索引
4. **测试**：每个阶段都需要测试验证
