# 前端管理界面技术设计文档

> 本文档描述 x-client 前端管理界面的设计方案

## 目录

1. [背景与目标](#1-背景与目标)
2. [整体架构](#2-整体架构)
3. [页面设计](#3-页面设计)
4. [组件设计](#4-组件设计)
5. [API 设计](#5-api-设计)
6. [技术栈](#6-技术栈)
7. [路由设计](#7-路由设计)

---

## 1. 背景与目标

### 1.1 问题

x-client 目前只提供简单的测试 UI (`ui-test/`) 用于聊天室功能验证，缺乏完整的管理界面来：
- 管理 Agent 配置
- 管理聊天室
- 查看消息历史
- 配置触发器
- 查看 Agent 状态

### 1.2 目标

1. **Agent 管理**：查看/编辑 Agent 配置，加载模板
2. **聊天室管理**：创建/加入/配置聊天室，管理成员
3. **消息历史**：查看聊天室消息历史，支持搜索
4. **触发器管理**：创建/编辑/启停触发器，查看触发记录
5. **系统状态**：Agent 在线状态，消息统计

---

## 2. 整体架构

### 2.1 架构图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         前端管理界面                                      │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │  React + TypeScript + Vite                                         │ │
│  │  ├── pages/                                                        │ │
│  │  │   ├── Dashboard.tsx        - 系统概览                            │ │
│  │  │   ├── AgentList.tsx        - Agent 列表                         │ │
│  │  │   ├── AgentDetail.tsx      - Agent 详情/配置                     │ │
│  │  │   ├── RoomList.tsx         - 聊天室列表                          │ │
│  │  │   ├── RoomDetail.tsx       - 聊天室详情/消息                     │ │
│  │  │   ├── TriggerList.tsx      - 触发器列表                          │ │
│  │  │   └── TriggerDetail.tsx    - 触发器详情/配置                     │ │
│  │  │                                                                  │ │
│  │  ├── components/                                                    │ │
│  │  │   ├── Layout.tsx           - 页面布局                            │ │
│  │  │   ├── Sidebar.tsx          - 侧边导航                            │ │
│  │  │   ├── ChatWindow.tsx        - 聊天窗口组件                        │ │
│  │  │   ├── TriggerEditor.tsx    - 触发器编辑器                        │ │
│  │  │   └── AgentCard.tsx        - Agent 卡片                          │ │
│  │  │                                                                  │ │
│  │  └── services/                                                      │ │
│  │       └── api.ts               - API 调用封装                       │ │
│  └───────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ REST API / WebSocket
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                      Coordinator HTTP (:8080)                            │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │  管理 API                                                           │ │
│  │  • GET/POST /api/agents         - Agent CRUD                       │ │
│  │  • GET/POST /api/rooms          - Room CRUD                        │ │
│  │  • GET /api/rooms/{id}/messages - 消息历史                         │ │
│  │  • GET/POST/PATCH/DELETE /api/triggers - 触发器管理                 │ │
│  │  • GET /api/stats               - 系统统计                          │ │
│  └───────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────┘
```

### 2.2 页面层级

```
/                           # 首页/仪表盘
├── /agents                 # Agent 列表
│   └── /agents/:id         # Agent 详情
│       ├── /agents/:id/settings    # Agent 配置
│       ├── /agents/:id/triggers    # 触发器管理
│       └── /agents/:id/memory      # 记忆管理
├── /rooms                  # 聊天室列表
│   └── /rooms/:id          # 聊天室详情
│       └── /rooms/:id/messages     # 消息历史
├── /triggers               # 全局触发器列表
└── /settings               # 系统设置
```

---

## 3. 页面设计

### 3.1 首页仪表盘 (Dashboard)

```
┌─────────────────────────────────────────────────────────────────────┐
│  X-Client 管理后台                                              [侧边栏]│
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐             │
│  │ Agents   │  │  Rooms   │  │ Messages │  │ Triggers │             │
│  │    5     │  │   12     │  │  1,234   │  │    8     │             │
│  │ 在线: 3  │  │ 活跃: 5  │  │ 今日: 56 │  │ 激活: 6  │             │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘             │
│                                                                      │
│  最近活动                                                            │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ [9:30] Agent-001 在 Room-A 发送消息                           │   │
│  │ [9:15] 触发器 daily_report 已触发                             │   │
│  │ [9:00] Agent-002 加入 Room-B                                  │   │
│  │ [8:45] 新消息: @Agent-001 帮忙看看这个文档                    │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                      │
│  Agent 状态                                                          │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                           │
│  │ ●在线    │  │ ○离线    │  │ ◐忙碌    │                           │
│  │ Agent-1 │  │ Agent-3 │  │ Agent-2 │                           │
│  │ Agent-2 │  │ Agent-5 │  │ Agent-4 │                           │
│  │ Agent-4 │  │          │  │          │                           │
│  └──────────┘  └──────────┘  └──────────┘                           │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.2 Agent 详情页 (AgentDetail)

```
┌─────────────────────────────────────────────────────────────────────┐
│  Agent-001                                    [编辑] [删除]         │
├─────────────────────────────────────────────────────────────────────┤
│  [配置] [触发器] [记忆] [消息]                                       │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  基本信息                                                            │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ Agent ID:    agent_001                                       │   │
│  │ 名称:        日报助手                                          │   │
│  │ 类型:        Claude                                           │   │
│  │ Endpoint:   http://localhost:8001                             │   │
│  │ 状态:        ● 在线                                            │   │
│  │ 模板:        /data/agent_001/soul.md                          │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                      │
│  配置                                                                │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ Max Memory Size:  [50] 条                                     │   │
│  │ Poll Interval:    [5] 秒                                     │   │
│  │ Enable Auto Reply: [✓]                                        │   │
│  │ [保存配置]                                                    │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.3 聊天室详情页 (RoomDetail)

```
┌─────────────────────────────────────────────────────────────────────┐
│  Room-A                              [设置] [清空历史]              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  Agent-1  9:30  今天的工作日报已经生成完成                    │   │
│  │          Agent-2 9:31  好的，我来看看                         │   │
│  │    用户  9:32  @Agent-1 能帮我检查下格式吗                    │   │
│  │          Agent-1 9:33  没问题，格式检查中...                  │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ [输入消息...]                                    [发送] [文件]│   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                      │
│  成员 (5)                                          [邀请成员]        │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ ● Agent-001 (日报助手)           ● Agent-002 (秘书)         │   │
│  │ ● Agent-003 (翻译助手)           ○ Agent-004 (离线)         │   │
│  │ 👤 用户 (当前用户)                                          │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.4 触发器管理页 (TriggerList)

```
┌─────────────────────────────────────────────────────────────────────┐
│  触发器管理                                          [+ 新建触发器]  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  筛选: [全部 ▼] [cron] [interval] [poll] [webhook]      [搜索...]   │
│                                                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │ 名称          类型      状态    下次触发        触发次数    │   │
│  ├─────────────────────────────────────────────────────────────┤   │
│  │ daily_report  cron     ● 启用  2026-07-01 09:00   25      │   │
│  │ check_status   interval ● 启用  2026-07-01 10:30   150     │   │
│  │ api_monitor    poll     ● 启用  2026-07-01 09:15   89      │   │
│  │ webhook_alert  webhook  ○ 停用  -                   12     │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                      │
│  [编辑] [启用/停用] [删除]                                           │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.5 新建触发器弹窗

```
┌─────────────────────────────────────────────────────────────────────┐
│  创建触发器                                                    [×]  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  触发器名称: [daily_report________________]                         │
│                                                                      │
│  触发器类型:                                                         │
│    ○ Cron 定时    ○ 单次触发    ● 间隔执行                           │
│    ○ HTTP 轮询    ○ Webhook     ○ 消息事件                          │
│                                                                      │
│  ─────────────────────────────────────────────────────────────────  │
│                                                                      │
│  配置 (间隔执行):                                                     │
│                                                                      │
│    间隔: [30] ▼ 分钟                                                │
│                                                                      │
│  ─────────────────────────────────────────────────────────────────  │
│                                                                      │
│  关联聊天室: [Room-A ▼]                                              │
│                                                                      │
│  触发原因: [每30分钟检查一次任务状态________________]                 │
│                                                                      │
│  冷却时间: [60] 秒                                                   │
│                                                                      │
│  最大触发次数: [无限制 ▼]                                            │
│                                                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                         [取消]  [创建]                      │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 4. 组件设计

### 4.1 布局组件

```tsx
// Layout.tsx - 页面布局
export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <div className="layout">
      <Sidebar />
      <main className="main-content">
        <Header />
        <div className="page-content">{children}</div>
      </main>
    </div>
  );
}

// Sidebar.tsx - 侧边导航
export function Sidebar() {
  const items = [
    { path: '/', icon: Dashboard, label: '仪表盘' },
    { path: '/agents', icon: Robot, label: 'Agent' },
    { path: '/rooms', icon: MessageCircle, label: '聊天室' },
    { path: '/triggers', icon: Clock, label: '触发器' },
    { path: '/settings', icon: Settings, label: '设置' },
  ];
  // ...
}
```

### 4.2 聊天组件

```tsx
// ChatWindow.tsx - 聊天窗口
interface ChatMessage {
  id: string;
  sender: string;
  senderType: 'user' | 'agent' | 'system';
  content: string;
  timestamp: Date;
  intent?: string;
}

export function ChatWindow({ roomId }: { roomId: string }) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState('');
  
  // WebSocket 接收新消息
  // 发送消息
  // 滚动到底部
  
  return (
    <div className="chat-window">
      <div className="message-list">
        {messages.map(msg => (
          <MessageBubble key={msg.id} message={msg} />
        ))}
      </div>
      <div className="message-input">
        <textarea 
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
        />
        <button onClick={sendMessage}>发送</button>
      </div>
    </div>
  );
}
```

### 4.3 触发器编辑器

```tsx
// TriggerEditor.tsx - 触发器编辑器
interface TriggerFormData {
  name: string;
  type: 'cron' | 'once' | 'interval' | 'poll' | 'webhook' | 'on_message';
  config: CronConfig | IntervalConfig | PollConfig | WebhookConfig | OnMessageConfig;
  reason: string;
  roomId?: string;
  cooldownSeconds: number;
  maxFires?: number;
}

export function TriggerEditor({ 
  trigger, 
  onSave, 
  onCancel 
}: {
  trigger?: Trigger;
  onSave: (data: TriggerFormData) => void;
  onCancel: () => void;
}) {
  const [formData, setFormData] = useState<TriggerFormData>(/* ... */);
  
  return (
    <div className="trigger-editor">
      {/* 名称输入 */}
      {/* 类型选择 */}
      {/* 动态配置表单 */}
      <ConfigForm type={formData.type} value={formData.config} onChange={/* ... */} />
      {/* 其他字段 */}
      <div className="actions">
        <button onClick={onCancel}>取消</button>
        <button onClick={() => onSave(formData)}>保存</button>
      </div>
    </div>
  );
}

// ConfigForm.tsx - 根据类型渲染配置表单
function CronConfigForm() {
  return (
    <div className="cron-config">
      <label>Cron 表达式:</label>
      <input 
        placeholder="0 9 * * 1-5"
        title="分 时 日 月 周"
      />
      <CronPreview expression={expr} />
    </div>
  );
}
```

### 4.4 Agent 卡片

```tsx
// AgentCard.tsx
interface Agent {
  id: string;
  name: string;
  endpoint: string;
  status: 'online' | 'offline' | 'busy';
  template?: string;
}

export function AgentCard({ agent, onClick }: { agent: Agent; onClick: () => void }) {
  const statusColor = {
    online: '#10b981',
    offline: '#6b7280',
    busy: '#f59e0b',
  };
  
  return (
    <div className="agent-card" onClick={onClick}>
      <div className="status-dot" style={{ background: statusColor[agent.status] }} />
      <div className="agent-info">
        <div className="agent-name">{agent.name}</div>
        <div className="agent-id">{agent.id}</div>
      </div>
    </div>
  );
}
```

---

## 5. API 设计

### 5.1 管理 API (Coordinator HTTP)

#### 5.1.1 系统统计

```
GET /api/stats
```

Response:
```json
{
  "agents": {
    "total": 5,
    "online": 3,
    "offline": 2
  },
  "rooms": {
    "total": 12,
    "active": 5
  },
  "messages": {
    "total": 1234,
    "today": 56
  },
  "triggers": {
    "total": 8,
    "enabled": 6
  }
}
```

#### 5.1.2 Agent 管理

```
GET /api/agents
POST /api/agents
GET /api/agents/{id}
PUT /api/agents/{id}
DELETE /api/agents/{id}
POST /api/agents/{id}/register  # 重新注册
```

#### 5.1.3 Room 管理

```
GET /api/rooms
POST /api/rooms
GET /api/rooms/{id}
PUT /api/rooms/{id}
DELETE /api/rooms/{id}
GET /api/rooms/{id}/messages?limit=50&offset=0
POST /api/rooms/{id}/members
DELETE /api/rooms/{id}/members/{user_id}
```

#### 5.1.4 消息管理

```
GET /api/messages?room_id=xxx&limit=50&offset=0&search=keyword
```

---

## 6. 技术栈

| 技术 | 用途 |
|------|------|
| React 19 | UI 框架 |
| TypeScript | 类型安全 |
| Vite | 构建工具 |
| React Router 7 | 路由管理 |
| TanStack Query | 数据请求/缓存 |
| Zustand | 状态管理 |
| Tailwind CSS | 样式框架 |
| @tabler/icons-react | 图标库 |
| date-fns | 日期处理 |

---

## 7. 路由设计

```tsx
// App.tsx
import { createBrowserRouter, RouterProvider, Navigate } from 'react-router-dom';

const router = createBrowserRouter([
  {
    path: '/',
    element: <Layout />,
    children: [
      { index: true, element: <Dashboard /> },
      { path: 'agents', element: <AgentList /> },
      { path: 'agents/:id', element: <AgentDetail /> },
      { path: 'agents/:id/settings', element: <AgentSettings /> },
      { path: 'agents/:id/triggers', element: <AgentTriggers /> },
      { path: 'rooms', element: <RoomList /> },
      { path: 'rooms/:id', element: <RoomDetail /> },
      { path: 'rooms/:id/messages', element: <RoomMessages /> },
      { path: 'triggers', element: <TriggerList /> },
      { path: 'settings', element: <Settings /> },
    ],
  },
  { path: '/login', element: <Login /> },
  { path: '*', element: <Navigate to="/" replace /> },
]);
```

---

## 8. 目录结构

```
frontend/
├── index.html
├── package.json
├── vite.config.ts
├── tsconfig.json
├── tailwind.config.js
├── src/
│   ├── main.tsx
│   ├── App.tsx
│   ├── index.css
│   ├── pages/
│   │   ├── Dashboard.tsx
│   │   ├── AgentList.tsx
│   │   ├── AgentDetail.tsx
│   │   ├── AgentSettings.tsx
│   │   ├── AgentTriggers.tsx
│   │   ├── RoomList.tsx
│   │   ├── RoomDetail.tsx
│   │   ├── RoomMessages.tsx
│   │   ├── TriggerList.tsx
│   │   ├── TriggerDetail.tsx
│   │   ├── Login.tsx
│   │   └── Settings.tsx
│   ├── components/
│   │   ├── Layout.tsx
│   │   ├── Sidebar.tsx
│   │   ├── Header.tsx
│   │   ├── ChatWindow.tsx
│   │   ├── MessageBubble.tsx
│   │   ├── TriggerEditor.tsx
│   │   ├── CronPicker.tsx
│   │   ├── AgentCard.tsx
│   │   ├── RoomCard.tsx
│   │   ├── StatusBadge.tsx
│   │   ├── ConfirmModal.tsx
│   │   └── Toast.tsx
│   ├── hooks/
│   │   ├── useWebSocket.ts
│   │   ├── useAgents.ts
│   │   ├── useRooms.ts
│   │   ├── useTriggers.ts
│   │   └── useMessages.ts
│   ├── services/
│   │   └── api.ts
│   ├── stores/
│   │   └── appStore.ts
│   ├── types/
│   │   └── index.ts
│   └── utils/
│       ├── formatters.ts
│       └── validators.ts
└── public/
    └── favicon.svg
```

---

## 9. WebSocket 实时通信

```tsx
// useWebSocket.ts
export function useWebSocket(onMessage: (msg: ChatMessage) => void) {
  const wsRef = useRef<WebSocket>();
  
  useEffect(() => {
    const ws = new WebSocket('ws://localhost:8080/ws/manage');
    
    ws.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      onMessage(msg);
    };
    
    ws.onclose = () => {
      // 重连逻辑
      setTimeout(() => connect(), 3000);
    };
    
    wsRef.current = ws;
    
    return () => ws.close();
  }, []);
  
  const send = (data: any) => wsRef.current?.send(JSON.stringify(data));
  
  return { send };
}
```

---

## 10. 与 Clawith 前端的差异

| 特性 | Clawith | x-client 前端 |
|------|---------|---------------|
| 框架 | React 19 | React 19 |
| 构建 | Vite | Vite |
| 路由 | React Router 7 | React Router 7 |
| 状态 | Zustand | Zustand |
| 数据请求 | TanStack Query | TanStack Query |
| 样式 | 自定义 CSS | Tailwind CSS |
| 组件库 | @tabler/icons-react | @tabler/icons-react |
| i18n | i18next | 初期中文 |
| 目标用户 | 企业管理员 | 开发者/测试者 |
| 复杂度 | 高 | 中低 |
