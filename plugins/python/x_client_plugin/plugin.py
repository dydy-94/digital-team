"""
X-Client Plugin 主类

工作目录结构：
    Agent 在聊天室中工作时，有独立的工作目录，按 {agent_id}/{room_id} 维度隔离。

    目录结构：
        ~/.x-client/workspace/{agent_id}/{room_id}/
        ├── inbox/messages/           # 接收到的消息缓存
        ├── uploads/{room_id}/        # 用户上传的文件
        ├── downloads/{room_id}/      # 下载的文件缓存
        └── reports/{room_id}/        # Agent 产出的报告

    Agent 在推理时应按需读取对应目录下的文件。
"""

import httpx
import json
from typing import List, Dict, Any, Optional
from dataclasses import dataclass


@dataclass
class ToolResult:
    """Tool 执行结果"""
    success: bool
    content: str
    error: Optional[str] = None


class XClientPlugin:
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
    
    # ============ Tool 定义 ============
    
    def get_tools(self) -> List[Dict[str, Any]]:
        """获取所有 Tool 定义"""
        return [
            {
                "name": "send_message_to_agent",
                "description": self._get_send_message_desc(),
                "input_schema": self._get_send_message_schema(),
            },
            {
                "name": "list_room_agents",
                "description": "查询当前聊天室的所有 Agent 成员及其角色、关系",
                "input_schema": {
                    "type": "object",
                    "properties": {
                        "room_id": {"type": "string", "description": "聊天室 ID"}
                    },
                    "required": ["room_id"]
                },
            },
            {
                "name": "get_agent_context",
                "description": "获取指定 Agent 的详细信息，包括同事关系、上下级关系",
                "input_schema": {
                    "type": "object",
                    "properties": {
                        "agent_id": {"type": "string", "description": "Agent ID"},
                        "room_id": {"type": "string", "description": "聊天室 ID"}
                    },
                    "required": ["agent_id", "room_id"]
                },
            },
            {
                "name": "create_task",
                "description": "创建一个任务，可分配给聊天室中的其他 Agent",
                "input_schema": {
                    "type": "object",
                    "properties": {
                        "title": {"type": "string", "description": "任务标题"},
                        "description": {"type": "string", "description": "任务描述"},
                        "assigned_to": {"type": "string", "description": "分配的 Agent ID"},
                        "room_id": {"type": "string", "description": "聊天室 ID"}
                    },
                    "required": ["title", "assigned_to", "room_id"]
                },
            },
            {
                "name": "query_task",
                "description": "查询任务状态和详情",
                "input_schema": {
                    "type": "object",
                    "properties": {
                        "task_id": {"type": "string", "description": "任务 ID"}
                    },
                    "required": ["task_id"]
                },
            },
            {
                "name": "delegate_task",
                "description": "向其他 Agent 委派任务，支持关注点列表",
                "input_schema": {
                    "type": "object",
                    "properties": {
                        "title": {"type": "string", "description": "任务标题"},
                        "description": {"type": "string", "description": "任务描述"},
                        "assigned_to": {"type": "string", "description": "被委派的 Agent ID"},
                        "room_id": {"type": "string", "description": "聊天室 ID"},
                        "focus_items": {
                            "type": "array",
                            "items": {"type": "string"},
                            "description": "关注点列表，如 ['UI设计', '后端实现']"
                        }
                    },
                    "required": ["title", "assigned_to", "room_id"]
                },
            },
            {
                "name": "upload_file",
                "description": "上传文件到聊天室，其他成员可下载",
                "input_schema": {
                    "type": "object",
                    "properties": {
                        "file_path": {"type": "string", "description": "要上传的文件路径"},
                        "file_name": {"type": "string", "description": "文件名（可选，默认使用原文件名）"},
                        "to_agent": {"type": "string", "description": "指定发送给哪个 Agent（可选）"},
                        "room_id": {"type": "string", "description": "聊天室 ID"}
                    },
                    "required": ["file_path", "room_id"]
                },
            },
            {
                "name": "download_file",
                "description": "根据 transfer_id 下载文件到工作区",
                "input_schema": {
                    "type": "object",
                    "properties": {
                        "transfer_id": {"type": "string", "description": "文件传输 ID"},
                        "save_path": {"type": "string", "description": "保存路径（可选，默认保存到工作区）"}
                    },
                    "required": ["transfer_id"]
                },
            },
        ]
    
    # ============ Tool 执行 ============
    
    async def execute(self, tool_name: str, arguments: Dict[str, Any]) -> ToolResult:
        """执行 Tool"""
        if tool_name == "send_message_to_agent":
            return await self._send_message_to_agent(**arguments)
        elif tool_name == "list_room_agents":
            return await self._list_room_agents(**arguments)
        elif tool_name == "get_agent_context":
            return await self._get_agent_context(**arguments)
        elif tool_name == "create_task":
            return await self._create_task(**arguments)
        elif tool_name == "query_task":
            return await self._query_task(**arguments)
        elif tool_name == "delegate_task":
            return await self._delegate_task(**arguments)
        elif tool_name == "upload_file":
            return await self._upload_file(**arguments)
        elif tool_name == "download_file":
            return await self._download_file(**arguments)
        else:
            return ToolResult(success=False, content="", error=f"Unknown tool: {tool_name}")
    
    # ============ Tool 实现 ============
    
    async def _send_message_to_agent(
        self,
        target_agent: str,
        message: str,
        room_id: str,
        intent: str = "DELEGATE"
    ) -> ToolResult:
        """
        向 Agent 发送消息
        
        Args:
            target_agent: 目标 Agent ID
            message: 消息内容
            room_id: 聊天室 ID
            intent: 消息意图 (DELEGATE/INFORM/QUERY)
        
        Returns:
            发送结果
        """
        async with httpx.AsyncClient(timeout=30.0) as client:
            try:
                response = await client.post(
                    f"{self.x_client_url}/api/send",
                    json={
                        "room_id": room_id,
                        "content": f"@{target_agent} {message}",
                        "mention_users": [target_agent],
                        "intent": intent
                    }
                )
                
                if response.status_code == 200:
                    return ToolResult(
                        success=True,
                        content=f"消息已发送给 {target_agent}，请等待回复..."
                    )
                else:
                    return ToolResult(
                        success=False,
                        content="",
                        error=f"发送失败: {response.status_code} - {response.text}"
                    )
                    
            except httpx.RequestError as e:
                return ToolResult(
                    success=False,
                    content="",
                    error=f"网络错误: {str(e)}"
                )
    
    async def _list_room_agents(self, room_id: str) -> ToolResult:
        """查询聊天室成员"""
        async with httpx.AsyncClient(timeout=10.0) as client:
            try:
                response = await client.get(
                    f"{self.x_client_url}/api/room/{room_id}/agents"
                )
                
                if response.status_code == 200:
                    data = response.json()
                    agents = data.get("agents", [])
                    
                    if not agents:
                        return ToolResult(
                            success=True,
                            content=f"聊天室 {room_id} 中没有 Agent 成员"
                        )
                    
                    output = f"聊天室 {room_id} 的成员:\n\n"
                    for agent in agents:
                        role = agent.get("role", "成员")
                        status = "在线" if agent.get("online") else "离线"
                        output += f"• {agent['agent_id']} - {role} [{status}]\n"
                        
                        # 显示关系
                        relations = agent.get("relations")
                        if relations:
                            colleagues = relations.get("colleagues", [])
                            if colleagues:
                                output += f"  同事: {', '.join(colleagues)}\n"
                    
                    return ToolResult(success=True, content=output)
                else:
                    return ToolResult(
                        success=False,
                        content="",
                        error=f"查询失败: {response.status_code}"
                    )
                    
            except Exception as e:
                return ToolResult(
                    success=False,
                    content="",
                    error=f"错误: {str(e)}"
                )
    
    async def _get_agent_context(self, agent_id: str, room_id: str) -> ToolResult:
        """获取 Agent 详细信息"""
        async with httpx.AsyncClient(timeout=10.0) as client:
            try:
                response = await client.get(
                    f"{self.x_client_url}/api/agent/context",
                    params={"agent_id": agent_id, "room_id": room_id}
                )
                
                if response.status_code == 200:
                    data = response.json()
                    ctx = data.get("context", {})
                    
                    output = f"Agent: {agent_id}\n\n"
                    
                    # 当前 Agent 信息
                    current = ctx.get("current_agent", {})
                    if current:
                        output += f"状态: {'在线' if current.get('online') else '离线'}\n"
                    
                    # 关系
                    relations = ctx.get("relations", {})
                    if relations:
                        output += "\n关系:\n"
                        
                        colleagues = relations.get("colleagues", [])
                        if colleagues:
                            output += f"  👥 同事: {', '.join(colleagues)}\n"
                        
                        superiors = relations.get("superiors", [])
                        if superiors:
                            output += f"  ⬆️ 上级: {', '.join(superiors)}\n"
                        
                        subordinates = relations.get("subordinates", [])
                        if subordinates:
                            output += f"  ⬇️ 下级: {', '.join(subordinates)}\n"
                    
                    # 聊天室成员
                    members = ctx.get("room_members", [])
                    if members:
                        output += f"\n聊天室成员 ({len(members)} 人):\n"
                        for m in members[:5]:  # 只显示前5个
                            status = "在线" if m.get("online") else "离线"
                            output += f"  • {m['agent_id']} [{status}]\n"
                        if len(members) > 5:
                            output += f"  ... 还有 {len(members) - 5} 人\n"
                    
                    return ToolResult(success=True, content=output)
                else:
                    return ToolResult(
                        success=False,
                        content="",
                        error=f"查询失败: {response.status_code}"
                    )
                    
            except Exception as e:
                return ToolResult(
                    success=False,
                    content="",
                    error=f"错误: {str(e)}"
                )
    
    async def _create_task(
        self,
        title: str,
        assigned_to: str,
        room_id: str,
        description: str = ""
    ) -> ToolResult:
        """创建任务"""
        async with httpx.AsyncClient(timeout=10.0) as client:
            try:
                response = await client.post(
                    f"{self.x_client_url}/api/task/create",
                    json={
                        "title": title,
                        "description": description,
                        "assigned_to": assigned_to,
                        "room_id": room_id
                    }
                )
                
                if response.status_code == 200:
                    data = response.json()
                    task_id = data.get("task_id")
                    return ToolResult(
                        success=True,
                        content=f"任务已创建! ✅\n任务ID: {task_id}\n分配给: {assigned_to}\n标题: {title}"
                    )
                else:
                    return ToolResult(
                        success=False,
                        content="",
                        error=f"创建失败: {response.status_code} - {response.text}"
                    )
                    
            except Exception as e:
                return ToolResult(
                    success=False,
                    content="",
                    error=f"错误: {str(e)}"
                )
    
    async def _query_task(self, task_id: str) -> ToolResult:
        """查询任务"""
        async with httpx.AsyncClient(timeout=10.0) as client:
            try:
                response = await client.get(
                    f"{self.x_client_url}/api/task/{task_id}"
                )
                
                if response.status_code == 200:
                    data = response.json()
                    task = data.get("task", {})
                    
                    if not task:
                        return ToolResult(
                            success=False,
                            content="",
                            error="任务不存在"
                        )
                    
                    output = f"📋 任务详情:\n"
                    output += f"  ID: {task_id}\n"
                    output += f"  标题: {task.get('title', 'N/A')}\n"
                    output += f"  状态: {task.get('status', 'N/A')}\n"
                    output += f"  分配给: {task.get('assigned_to', 'N/A')}\n"
                    output += f"  创建者: {task.get('created_by', 'N/A')}\n"
                    
                    return ToolResult(success=True, content=output)
                else:
                    return ToolResult(
                        success=False,
                        content="",
                        error=f"查询失败: {response.status_code}"
                    )
                    
            except Exception as e:
                return ToolResult(
                    success=False,
                    content="",
                    error=f"错误: {str(e)}"
                )

    async def _delegate_task(
        self,
        title: str,
        assigned_to: str,
        room_id: str,
        description: str = "",
        focus_items: Optional[List[str]] = None
    ) -> ToolResult:
        """
        向其他 Agent 委派任务

        Args:
            title: 任务标题
            assigned_to: 被委派的 Agent ID
            room_id: 聊天室 ID
            description: 任务描述
            focus_items: 关注点列表，如 ['UI设计', '后端实现']
        """
        import uuid
        import time

        # 构造消息内容，遵循 /delegate 命令格式
        content = f"/delegate {title} to {assigned_to}"
        if description:
            content += f"\n{description}"
        if focus_items:
            content += " with focus"
            for item in focus_items:
                content += f" [ ] {item}"

        async with httpx.AsyncClient(timeout=30.0) as client:
            try:
                response = await client.post(
                    f"{self.x_client_url}/skill/delegate",
                    json={
                        "room_id": room_id,
                        "sender": "plugin",  # plugin 代为发送
                        "content": content,
                        "msg_id": str(uuid.uuid4()),
                        "intent": "DELEGATE",
                        "timestamp": int(time.time() * 1000)
                    }
                )

                if response.status_code == 200:
                    data = response.json()
                    if data.get("status") == "duplicate":
                        return ToolResult(
                            success=True,
                            content=f"任务已重复提交，跳过处理"
                        )
                    return ToolResult(
                        success=True,
                        content=f"任务已委派给 {assigned_to} ✅\n标题: {title}\n分配给: {assigned_to}"
                    )
                else:
                    return ToolResult(
                        success=False,
                        content="",
                        error=f"委派失败: {response.status_code} - {response.text}"
                    )

            except httpx.RequestError as e:
                return ToolResult(
                    success=False,
                    content="",
                    error=f"网络错误: {str(e)}"
                )

    async def _upload_file(
        self,
        file_path: str,
        room_id: str,
        file_name: Optional[str] = None,
        to_agent: Optional[str] = None
    ) -> ToolResult:
        """
        上传文件到聊天室

        Args:
            file_path: 要上传的文件路径
            room_id: 聊天室 ID
            file_name: 文件名（可选，默认使用原文件名）
            to_agent: 指定发送给哪个 Agent（可选）
        """
        import os
        import uuid

        # 读取文件
        if not os.path.exists(file_path):
            return ToolResult(
                success=False,
                content="",
                error=f"文件不存在: {file_path}"
            )

        if file_name is None:
            file_name = os.path.basename(file_path)

        file_size = os.path.getsize(file_path)
        with open(file_path, "rb") as f:
            file_data = f.read()

        async with httpx.AsyncClient(timeout=60.0) as client:
            try:
                # 使用 multipart/form-data 上传文件和元数据
                files = {"file": (file_name, file_data)}
                data = {
                    "file_name": file_name,
                    "room_id": room_id,
                    "to_agent": to_agent or ""
                }
                response = await client.post(
                    f"{self.x_client_url}/api/file/upload",
                    files=files,
                    data=data
                )

                if response.status_code != 200:
                    return ToolResult(
                        success=False,
                        content="",
                        error=f"上传失败: {response.status_code} - {response.text}"
                    )

                resp_data = response.json()
                transfer_id = resp_data.get("transfer_id")

                return ToolResult(
                    success=True,
                    content=f"文件上传成功 ✅\n文件名: {file_name}\n大小: {file_size} bytes\n传输ID: {transfer_id}\n其他成员可使用 transfer_id 下载此文件"
                )

            except httpx.RequestError as e:
                return ToolResult(
                    success=False,
                    content="",
                    error=f"网络错误: {str(e)}"
                )

    async def _download_file(
        self,
        transfer_id: str,
        save_path: Optional[str] = None
    ) -> ToolResult:
        """
        根据 transfer_id 下载文件到工作区

        Args:
            transfer_id: 文件传输 ID
            save_path: 保存路径（可选，默认保存到工作区 downloads 目录）
        """
        import os

        if save_path is None:
            save_path = os.path.join(
                os.path.expanduser("~/.x-client"),
                "downloads",
                transfer_id
            )

        os.makedirs(os.path.dirname(save_path), exist_ok=True)

        async with httpx.AsyncClient(timeout=60.0) as client:
            try:
                response = await client.get(
                    f"{self.x_client_url}/api/file/download?transfer_id={transfer_id}",
                    follow_redirects=True
                )

                if response.status_code == 200:
                    content = response.content
                    with open(save_path, "wb") as f:
                        f.write(content)

                    return ToolResult(
                        success=True,
                        content=f"文件下载成功 ✅\n传输ID: {transfer_id}\n保存路径: {save_path}\n大小: {len(content)} bytes"
                    )
                else:
                    return ToolResult(
                        success=False,
                        content="",
                        error=f"下载失败: {response.status_code}"
                    )

            except httpx.RequestError as e:
                return ToolResult(
                    success=False,
                    content="",
                    error=f"网络错误: {str(e)}"
                )

    # ============ Tool 描述辅助方法 ============
    
    def _get_send_message_desc(self) -> str:
        return """向聊天室中的指定 Agent 发送消息并等待回复。

使用场景：
- 需要其他 Agent 的帮助或专业意见
- 需要委托任务给其他 Agent
- 需要询问其他 Agent 相关信息
- 需要发送文件（配合 upload_file 使用）

发送文件流程：
1. 先调用 upload_file 上传文件，获得 transfer_id
2. 调用 send_message 时 message 中包含 transfer_id，intent 设为 "FILE"

注意：消息会自动 @ 目标 Agent，目标 Agent 会收到通知并回复。"""
    
    def _get_send_message_schema(self) -> dict:
        return {
            "type": "object",
            "properties": {
                "target_agent": {
                    "type": "string",
                    "description": "目标 Agent 的 ID，例如 'agent-002'"
                },
                "message": {
                    "type": "string",
                    "description": "你要发送的消息内容"
                },
                "room_id": {
                    "type": "string",
                    "description": "聊天室 ID"
                },
                "intent": {
                    "type": "string",
                    "enum": ["DELEGATE", "INFORM", "QUERY"],
                    "description": "消息意图，DELEGATE=委托任务，INFORM=通知，QUERY=询问",
                    "default": "DELEGATE"
                }
            },
            "required": ["target_agent", "message", "room_id"]
        }
