# 前端管理界面实施步骤

> 本文档描述 x-client 前端管理界面的详细实施步骤

## 目录

1. [实施阶段总览](#1-实施阶段总览)
2. [阶段一：项目初始化](#2-阶段一项目初始化)
3. [阶段二：核心组件](#3-阶段二核心组件)
4. [阶段三：页面开发](#4-阶段三页面开发)
5. [阶段四：API 集成](#5-阶段四api-集成)
6. [阶段五：实时通信](#6-阶段五实时通信)

---

## 1. 实施阶段总览

| 阶段 | 内容 | 优先级 | 预计工时 |
|------|------|--------|----------|
| Phase 1 | 项目初始化 | P0 | 0.5 天 |
| Phase 2 | 核心组件 | P0 | 1 天 |
| Phase 3 | 页面开发 | P0 | 2 天 |
| Phase 4 | API 集成 | P1 | 1 天 |
| Phase 5 | 实时通信 | P1 | 0.5 天 |

---

## 2. 阶段一：项目初始化

### 2.1 创建项目结构

```bash
mkdir -p frontend/src/{pages,components,hooks,services,stores,types,utils}
mkdir -p frontend/public
```

### 2.2 初始化 package.json

```json
{
  "name": "x-client-admin",
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "react": "^19.0.0",
    "react-dom": "^19.0.0",
    "react-router-dom": "^7.0.0",
    "@tanstack/react-query": "^5.0.0",
    "zustand": "^5.0.0",
    "@tabler/icons-react": "^3.0.0",
    "date-fns": "^4.0.0"
  },
  "devDependencies": {
    "@types/react": "^19.0.0",
    "@types/react-dom": "^19.0.0",
    "@vitejs/plugin-react": "^4.0.0",
    "autoprefixer": "^10.4.0",
    "postcss": "^8.4.0",
    "tailwindcss": "^4.0.0",
    "typescript": "^5.0.0",
    "vite": "^6.0.0"
  }
}
```

### 2.3 配置文件

**vite.config.ts:**
```ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
});
```

**tailwind.config.js:**
```js
/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {},
  },
  plugins: [],
};
```

**tsconfig.json:**
```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true
  },
  "include": ["src"]
}
```

### 2.4 入口文件

**index.html:**
```html
<!DOCTYPE html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>X-Client 管理后台</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

**src/main.tsx:**
```tsx
import React from 'react';
import ReactDOM from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from 'react-router-dom';
import { router } from './App';
import './index.css';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 1000 * 60,
      retry: 1,
    },
  },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>
);
```

**src/index.css:**
```css
@tailwind base;
@tailwind components;
@tailwind utilities;

:root {
  --bg-primary: #0f172a;
  --bg-secondary: #1e293b;
  --text-primary: #f8fafc;
  --text-secondary: #94a3b8;
  --accent: #3b82f6;
}

body {
  @apply bg-slate-900 text-slate-100;
}
```

---

## 3. 阶段二：核心组件

### 3.1 类型定义

**src/types/index.ts:**
```ts
// Agent 类型
export interface Agent {
  id: string;
  name: string;
  endpoint: string;
  status: 'online' | 'offline' | 'busy';
  template?: string;
  created_at: number;
}

// Room 类型
export interface Room {
  id: string;
  name: string;
  description?: string;
  member_count: number;
  created_at: number;
}

// Message 类型
export interface Message {
  id: string;
  room_id: string;
  sender_id: string;
  sender_type: 'user' | 'agent' | 'system';
  sender_name: string;
  content: string;
  intent?: string;
  created_at: number;
}

// Trigger 类型
export interface Trigger {
  id: string;
  agent_id: string;
  name: string;
  type: 'cron' | 'interval' | 'poll' | 'webhook' | 'once' | 'on_message';
  config: Record<string, unknown>;
  reason: string;
  room_id: string;
  is_enabled: boolean;
  last_fired_at?: number;
  next_fire_at?: number;
  fire_count: number;
  max_fires?: number;
  cooldown_seconds: number;
  created_at: number;
}

// Stats 类型
export interface Stats {
  agents: { total: number; online: number; offline: number };
  rooms: { total: number; active: number };
  messages: { total: number; today: number };
  triggers: { total: number; enabled: number };
}
```

### 3.2 状态管理

**src/stores/appStore.ts:**
```ts
import { create } from 'zustand';

interface AppState {
  // 侧边栏折叠状态
  sidebarCollapsed: boolean;
  toggleSidebar: () => void;
  
  // 选中的 Agent/Room
  selectedAgentId: string | null;
  setSelectedAgent: (id: string | null) => void;
  
  selectedRoomId: string | null;
  setSelectedRoom: (id: string | null) => void;
  
  // WebSocket 连接
  wsConnected: boolean;
  setWsConnected: (connected: boolean) => void;
}

export const useAppStore = create<AppState>((set) => ({
  sidebarCollapsed: false,
  toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
  
  selectedAgentId: null,
  setSelectedAgent: (id) => set({ selectedAgentId: id }),
  
  selectedRoomId: null,
  setSelectedRoom: (id) => set({ selectedRoomId: id }),
  
  wsConnected: false,
  setWsConnected: (connected) => set({ wsConnected: connected }),
}));
```

### 3.3 API 服务

**src/services/api.ts:**
```ts
const BASE_URL = '/api';

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options?.headers,
    },
  });
  
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}: ${await res.text()}`);
  }
  
  return res.json();
}

// Agent API
export const agentApi = {
  list: () => request<Agent[]>('/agents'),
  get: (id: string) => request<Agent>(`/agents/${id}`),
  create: (data: Partial<Agent>) => request<Agent>('/agents', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: string, data: Partial<Agent>) => request<Agent>(`/agents/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: string) => request<void>(`/agents/${id}`, { method: 'DELETE' }),
};

// Room API
export const roomApi = {
  list: () => request<Room[]>('/rooms'),
  get: (id: string) => request<Room>(`/rooms/${id}`),
  create: (data: Partial<Room>) => request<Room>('/rooms', { method: 'POST', body: JSON.stringify(data) }),
  getMessages: (id: string, limit = 50, offset = 0) => 
    request<Message[]>(`/rooms/${id}/messages?limit=${limit}&offset=${offset}`),
};

// Trigger API
export const triggerApi = {
  list: (agentId?: string) => 
    request<Trigger[]>(agentId ? `/triggers?agent_id=${agentId}` : '/triggers'),
  get: (id: string) => request<Trigger>(`/triggers/${id}`),
  create: (data: Partial<Trigger>) => 
    request<Trigger>('/triggers/register', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: string, data: Partial<Trigger>) => 
    request<Trigger>(`/triggers/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  delete: (id: string) => request<void>(`/triggers/${id}`, { method: 'DELETE' }),
};

// Stats API
export const statsApi = {
  get: () => request<Stats>('/stats'),
};
```

### 3.4 布局组件

**src/components/Layout.tsx:**
```tsx
import { Outlet } from 'react-router-dom';
import { Sidebar } from './Sidebar';
import { Header } from './Header';
import { useAppStore } from '../stores/appStore';

export function Layout() {
  const { sidebarCollapsed } = useAppStore();
  
  return (
    <div className="flex h-screen">
      <Sidebar />
      <div className={`flex-1 flex flex-col ${sidebarCollapsed ? 'ml-16' : 'ml-64'}`}>
        <Header />
        <main className="flex-1 overflow-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
```

**src/components/Sidebar.tsx:**
```tsx
import { Link, useLocation } from 'react-router-dom';
import { 
  IconLayoutDashboard, 
  IconRobot, 
  IconMessageCircle, 
  IconClock,
  IconSettings,
} from '@tabler/icons-react';
import { useAppStore } from '../stores/appStore';

const menuItems = [
  { path: '/', icon: IconLayoutDashboard, label: '仪表盘' },
  { path: '/agents', icon: IconRobot, label: 'Agent' },
  { path: '/rooms', icon: IconMessageCircle, label: '聊天室' },
  { path: '/triggers', icon: IconClock, label: '触发器' },
  { path: '/settings', icon: IconSettings, label: '设置' },
];

export function Sidebar() {
  const location = useLocation();
  const { sidebarCollapsed } = useAppStore();
  
  return (
    <aside className={`fixed left-0 top-0 h-screen bg-slate-800 transition-all ${sidebarCollapsed ? 'w-16' : 'w-64'}`}>
      <div className="p-4 border-b border-slate-700">
        <h1 className={`font-bold text-lg ${sidebarCollapsed ? 'hidden' : 'block'}`}>X-Client</h1>
      </div>
      
      <nav className="p-2">
        {menuItems.map((item) => {
          const Icon = item.icon;
          const isActive = location.pathname === item.path || 
            (item.path !== '/' && location.pathname.startsWith(item.path));
          
          return (
            <Link
              key={item.path}
              to={item.path}
              className={`flex items-center gap-3 px-3 py-2 rounded-lg mb-1 transition-colors
                ${isActive ? 'bg-blue-600 text-white' : 'text-slate-400 hover:bg-slate-700'}`}
            >
              <Icon size={20} />
              {!sidebarCollapsed && <span>{item.label}</span>}
            </Link>
          );
        })}
      </nav>
    </aside>
  );
}
```

**src/components/Header.tsx:**
```tsx
import { IconMenu2 } from '@tabler/icons-react';
import { useAppStore } from '../stores/appStore';

export function Header() {
  const { toggleSidebar } = useAppStore();
  
  return (
    <header className="h-14 bg-slate-800 border-b border-slate-700 flex items-center px-4">
      <button onClick={toggleSidebar} className="p-2 hover:bg-slate-700 rounded">
        <IconMenu2 size={20} />
      </button>
      
      <div className="ml-auto flex items-center gap-4">
        <span className="text-sm text-slate-400">X-Client Admin</span>
      </div>
    </header>
  );
}
```

---

## 4. 阶段三：页面开发

### 4.1 路由配置

**src/App.tsx:**
```tsx
import { createBrowserRouter } from 'react-router-dom';
import { Layout } from './components/Layout';
import { Dashboard } from './pages/Dashboard';
import { AgentList } from './pages/AgentList';
import { AgentDetail } from './pages/AgentDetail';
import { RoomList } from './pages/RoomList';
import { RoomDetail } from './pages/RoomDetail';
import { TriggerList } from './pages/TriggerList';
import { TriggerDetail } from './pages/TriggerDetail';
import { Settings } from './pages/Settings';

export const router = createBrowserRouter([
  {
    path: '/',
    element: <Layout />,
    children: [
      { index: true, element: <Dashboard /> },
      { path: 'agents', element: <AgentList /> },
      { path: 'agents/:id', element: <AgentDetail /> },
      { path: 'rooms', element: <RoomList /> },
      { path: 'rooms/:id', element: <RoomDetail /> },
      { path: 'rooms/:id/messages', element: <RoomDetail /> },
      { path: 'triggers', element: <TriggerList /> },
      { path: 'triggers/:id', element: <TriggerDetail /> },
      { path: 'settings', element: <Settings /> },
    ],
  },
]);
```

### 4.2 Dashboard 页面

**src/pages/Dashboard.tsx:**
```tsx
import { useQuery } from '@tanstack/react-query';
import { IconRobot, IconMessageCircle, IconClock } from '@tabler/icons-react';
import { statsApi } from '../services/api';

export function Dashboard() {
  const { data: stats, isLoading } = useQuery({
    queryKey: ['stats'],
    queryFn: statsApi.get,
    refetchInterval: 30000,
  });
  
  if (isLoading) {
    return <div className="animate-pulse">加载中...</div>;
  }
  
  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">仪表盘</h1>
      
      <div className="grid grid-cols-4 gap-6 mb-8">
        <StatCard 
          title="Agent" 
          value={stats?.agents.total ?? 0} 
          subtitle={`在线: ${stats?.agents.online ?? 0}`}
          icon={<IconRobot size={24} />}
          color="blue"
        />
        <StatCard 
          title="聊天室" 
          value={stats?.rooms.total ?? 0} 
          subtitle={`活跃: ${stats?.rooms.active ?? 0}`}
          icon={<IconMessageCircle size={24} />}
          color="green"
        />
        <StatCard 
          title="消息" 
          value={stats?.messages.total ?? 0} 
          subtitle={`今日: ${stats?.messages.today ?? 0}`}
          icon={<IconMessageCircle size={24} />}
          color="purple"
        />
        <StatCard 
          title="触发器" 
          value={stats?.triggers.total ?? 0} 
          subtitle={`启用: ${stats?.triggers.enabled ?? 0}`}
          icon={<IconClock size={24} />}
          color="orange"
        />
      </div>
      
      <div className="bg-slate-800 rounded-lg p-6">
        <h2 className="text-lg font-semibold mb-4">最近活动</h2>
        <div className="space-y-3">
          <ActivityItem time="9:30" content="Agent-001 在 Room-A 发送消息" />
          <ActivityItem time="9:15" content="触发器 daily_report 已触发" />
          <ActivityItem time="9:00" content="Agent-002 加入 Room-B" />
        </div>
      </div>
    </div>
  );
}

function StatCard({ title, value, subtitle, icon, color }: any) {
  const colors: Record<string, string> = {
    blue: 'bg-blue-500/20 text-blue-400 border-blue-500/30',
    green: 'bg-green-500/20 text-green-400 border-green-500/30',
    purple: 'bg-purple-500/20 text-purple-400 border-purple-500/30',
    orange: 'bg-orange-500/20 text-orange-400 border-orange-500/30',
  };
  
  return (
    <div className={`rounded-lg p-4 border ${colors[color]}`}>
      <div className="flex items-center justify-between mb-2">
        <span className="text-sm opacity-75">{title}</span>
        {icon}
      </div>
      <div className="text-3xl font-bold">{value}</div>
      <div className="text-sm opacity-75">{subtitle}</div>
    </div>
  );
}

function ActivityItem({ time, content }: { time: string; content: string }) {
  return (
    <div className="flex gap-3 text-sm">
      <span className="text-slate-500 w-12">{time}</span>
      <span className="text-slate-300">{content}</span>
    </div>
  );
}
```

### 4.3 Agent 列表页面

**src/pages/AgentList.tsx:**
```tsx
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { IconPlus, IconRefresh } from '@tabler/icons-react';
import { agentApi } from '../services/api';

export function AgentList() {
  const { data: agents, isLoading, refetch } = useQuery({
    queryKey: ['agents'],
    queryFn: agentApi.list,
  });
  
  return (
    <div>
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-2xl font-bold">Agent 管理</h1>
        <div className="flex gap-2">
          <button onClick={() => refetch()} className="btn btn-secondary">
            <IconRefresh size={18} />
          </button>
          <Link to="/agents/new" className="btn btn-primary">
            <IconPlus size={18} />
            新建 Agent
          </Link>
        </div>
      </div>
      
      {isLoading ? (
        <div className="animate-pulse">加载中...</div>
      ) : (
        <div className="grid grid-cols-3 gap-4">
          {agents?.map((agent) => (
            <Link key={agent.id} to={`/agents/${agent.id}`}>
              <AgentCard agent={agent} />
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}

function AgentCard({ agent }: { agent: any }) {
  const statusColors = {
    online: 'bg-green-500',
    offline: 'bg-slate-500',
    busy: 'bg-yellow-500',
  };
  
  return (
    <div className="bg-slate-800 rounded-lg p-4 hover:bg-slate-700 transition-colors">
      <div className="flex items-center gap-3 mb-3">
        <div className={`w-3 h-3 rounded-full ${statusColors[agent.status]}`} />
        <span className="font-semibold">{agent.name}</span>
      </div>
      <div className="text-sm text-slate-400 space-y-1">
        <div>ID: {agent.id}</div>
        <div>Endpoint: {agent.endpoint}</div>
      </div>
    </div>
  );
}
```

### 4.4 聊天室页面

**src/pages/RoomDetail.tsx:**
```tsx
import { useParams, Link } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useState, useRef, useEffect } from 'react';
import { roomApi } from '../services/api';
import { MessageBubble } from '../components/MessageBubble';

export function RoomDetail() {
  const { id } = useParams<{ id: string }>();
  const queryClient = useQueryClient();
  const [message, setMessage] = useState('');
  const messagesEndRef = useRef<HTMLDivElement>(null);
  
  const { data: room } = useQuery({
    queryKey: ['room', id],
    queryFn: () => roomApi.get(id!),
    enabled: !!id,
  });
  
  const { data: messages } = useQuery({
    queryKey: ['messages', id],
    queryFn: () => roomApi.getMessages(id!),
    enabled: !!id,
    refetchInterval: 5000,
  });
  
  const sendMutation = useMutation({
    mutationFn: (content: string) => 
      roomApi.sendMessage(id!, content),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['messages', id] });
      setMessage('');
    },
  });
  
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);
  
  const handleSend = () => {
    if (message.trim()) {
      sendMutation.mutate(message);
    }
  };
  
  return (
    <div className="h-full flex flex-col">
      <div className="flex justify-between items-center mb-4">
        <h1 className="text-2xl font-bold">{room?.name}</h1>
        <Link to={`/rooms/${id}/messages`} className="btn btn-secondary">
          查看历史
        </Link>
      </div>
      
      <div className="flex-1 bg-slate-800 rounded-lg overflow-hidden flex flex-col">
        <div className="flex-1 overflow-y-auto p-4 space-y-3">
          {messages?.map((msg) => (
            <MessageBubble key={msg.id} message={msg} />
          ))}
          <div ref={messagesEndRef} />
        </div>
        
        <div className="p-4 border-t border-slate-700">
          <div className="flex gap-2">
            <input
              type="text"
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleSend()}
              placeholder="输入消息..."
              className="flex-1 bg-slate-700 rounded-lg px-4 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
            <button 
              onClick={handleSend}
              disabled={sendMutation.isPending}
              className="btn btn-primary"
            >
              发送
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
```

### 4.5 触发器列表页面

**src/pages/TriggerList.tsx:**
```tsx
import { Link } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { IconPlus, IconTrash } from '@tabler/icons-react';
import { triggerApi } from '../services/api';
import { formatDistanceToNow } from 'date-fns';

export function TriggerList() {
  const queryClient = useQueryClient();
  
  const { data: triggers, isLoading } = useQuery({
    queryKey: ['triggers'],
    queryFn: () => triggerApi.list(),
  });
  
  const deleteMutation = useMutation({
    mutationFn: (id: string) => triggerApi.delete(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['triggers'] }),
  });
  
  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      triggerApi.update(id, { is_enabled: enabled }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['triggers'] }),
  });
  
  return (
    <div>
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-2xl font-bold">触发器管理</h1>
        <Link to="/triggers/new" className="btn btn-primary">
          <IconPlus size={18} />
          新建触发器
        </Link>
      </div>
      
      <div className="bg-slate-800 rounded-lg overflow-hidden">
        <table className="w-full">
          <thead className="bg-slate-700">
            <tr>
              <th className="px-4 py-3 text-left">名称</th>
              <th className="px-4 py-3 text-left">类型</th>
              <th className="px-4 py-3 text-left">状态</th>
              <th className="px-4 py-3 text-left">下次触发</th>
              <th className="px-4 py-3 text-left">触发次数</th>
              <th className="px-4 py-3 text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            {triggers?.map((trigger) => (
              <tr key={trigger.id} className="border-t border-slate-700 hover:bg-slate-700/50">
                <td className="px-4 py-3">
                  <Link to={`/triggers/${trigger.id}`} className="text-blue-400 hover:underline">
                    {trigger.name}
                  </Link>
                </td>
                <td className="px-4 py-3">
                  <span className="px-2 py-1 rounded text-xs bg-slate-600">
                    {trigger.type}
                  </span>
                </td>
                <td className="px-4 py-3">
                  <button
                    onClick={() => toggleMutation.mutate({ id: trigger.id, enabled: !trigger.is_enabled })}
                    className={`w-12 rounded-full h-6 flex items-center ${
                      trigger.is_enabled ? 'bg-green-500' : 'bg-slate-600'
                    }`}
                  >
                    <div className={`w-4 h-4 rounded-full bg-white transition-transform ${
                      trigger.is_enabled ? 'translate-x-7' : 'translate-x-1'
                    }`} />
                  </button>
                </td>
                <td className="px-4 py-3 text-slate-400">
                  {trigger.next_fire_at 
                    ? formatDistanceToNow(new Date(trigger.next_fire_at), { addSuffix: true })
                    : '-'}
                </td>
                <td className="px-4 py-3">{trigger.fire_count}</td>
                <td className="px-4 py-3 text-right">
                  <button
                    onClick={() => deleteMutation.mutate(trigger.id)}
                    className="p-2 hover:bg-red-500/20 rounded text-red-400"
                  >
                    <IconTrash size={18} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
```

### 4.6 触发器编辑器

**src/pages/TriggerDetail.tsx:**
```tsx
import { useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { triggerApi, roomApi } from '../services/api';

export function TriggerDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const isNew = id === 'new';
  
  const [form, setForm] = useState({
    name: '',
    type: 'interval' as const,
    config: { minutes: 30 },
    reason: '',
    room_id: '',
    cooldown_seconds: 60,
  });
  
  const { data: rooms } = useQuery({
    queryKey: ['rooms'],
    queryFn: roomApi.list,
  });
  
  const { data: trigger } = useQuery({
    queryKey: ['trigger', id],
    queryFn: () => triggerApi.get(id!),
    enabled: !isNew && !!id,
  });
  
  const saveMutation = useMutation({
    mutationFn: (data: typeof form) => 
      isNew ? triggerApi.create(data as any) : triggerApi.update(id!, data as any),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['triggers'] });
      navigate('/triggers');
    },
  });
  
  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    saveMutation.mutate(form);
  };
  
  return (
    <div className="max-w-2xl">
      <h1 className="text-2xl font-bold mb-6">
        {isNew ? '创建触发器' : '编辑触发器'}
      </h1>
      
      <form onSubmit={handleSubmit} className="bg-slate-800 rounded-lg p-6 space-y-4">
        <div>
          <label className="block text-sm mb-1">名称</label>
          <input
            type="text"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            className="w-full bg-slate-700 rounded px-3 py-2"
            required
          />
        </div>
        
        <div>
          <label className="block text-sm mb-2">类型</label>
          <div className="flex gap-4">
            {(['cron', 'interval', 'poll', 'webhook', 'once'] as const).map((type) => (
              <label key={type} className="flex items-center gap-2">
                <input
                  type="radio"
                  checked={form.type === type}
                  onChange={() => setForm({ ...form, type })}
                />
                {type}
              </label>
            ))}
          </div>
        </div>
        
        {form.type === 'interval' && (
          <div>
            <label className="block text-sm mb-1">间隔 (分钟)</label>
            <input
              type="number"
              value={(form.config as any).minutes || 30}
              onChange={(e) => setForm({ ...form, config: { minutes: parseInt(e.target.value) } })}
              className="w-32 bg-slate-700 rounded px-3 py-2"
              min={1}
            />
          </div>
        )}
        
        {form.type === 'cron' && (
          <div>
            <label className="block text-sm mb-1">Cron 表达式</label>
            <input
              type="text"
              value={(form.config as any).expr || ''}
              onChange={(e) => setForm({ ...form, config: { expr: e.target.value } })}
              placeholder="0 9 * * 1-5"
              className="w-full bg-slate-700 rounded px-3 py-2"
            />
          </div>
        )}
        
        <div>
          <label className="block text-sm mb-1">关联聊天室</label>
          <select
            value={form.room_id}
            onChange={(e) => setForm({ ...form, room_id: e.target.value })}
            className="w-full bg-slate-700 rounded px-3 py-2"
          >
            <option value="">选择聊天室</option>
            {rooms?.map((room) => (
              <option key={room.id} value={room.id}>{room.name}</option>
            ))}
          </select>
        </div>
        
        <div>
          <label className="block text-sm mb-1">触发原因</label>
          <input
            type="text"
            value={form.reason}
            onChange={(e) => setForm({ ...form, reason: e.target.value })}
            className="w-full bg-slate-700 rounded px-3 py-2"
          />
        </div>
        
        <div className="flex gap-4 pt-4">
          <button type="submit" className="btn btn-primary">
            {isNew ? '创建' : '保存'}
          </button>
          <button 
            type="button" 
            onClick={() => navigate('/triggers')}
            className="btn btn-secondary"
          >
            取消
          </button>
        </div>
      </form>
    </div>
  );
}
```

---

## 5. 阶段四：API 集成

### 5.1 Coordinator API 扩展

在 Coordinator 中添加管理 API:

```go
// coordinator-http/handler.go

// GetStats 获取系统统计
func (h *Handler) GetStats(c *gin.Context) {
    stats, err := h.storage.GetStats()
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    c.JSON(200, stats)
}

// ListAgents 列出所有 Agent
func (h *Handler) ListAgents(c *gin.Context) {
    agents, err := h.storage.ListAgents()
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    c.JSON(200, agents)
}

// GetRoomMessages 获取聊天室消息
func (h *Handler) GetRoomMessages(c *gin.Context) {
    roomID := c.Param("id")
    limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
    offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
    
    messages, err := h.storage.GetRoomMessages(roomID, limit, offset)
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    c.JSON(200, messages)
}
```

### 5.2 路由注册

```go
// coordinator-http/main.go

func main() {
    // ...
    
    r := gin.Default()
    
    // 管理 API
    admin := r.Group("/api")
    {
        admin.GET("/stats", h.GetStats)
        admin.GET("/agents", h.ListAgents)
        admin.GET("/agents/:id", h.GetAgent)
        admin.POST("/rooms", h.CreateRoom)
        admin.GET("/rooms", h.ListRooms)
        admin.GET("/rooms/:id", h.GetRoom)
        admin.GET("/rooms/:id/messages", h.GetRoomMessages)
        admin.POST("/rooms/:id/messages", h.SendMessage)
    }
    
    // 触发器路由
    triggers := r.Group("/api/triggers")
    {
        triggers.GET("", h.ListTriggers)
        triggers.GET("/:id", h.GetTrigger)
        triggers.POST("/register", h.CreateTrigger)
        triggers.PATCH("/:id", h.UpdateTrigger)
        triggers.DELETE("/:id", h.DeleteTrigger)
    }
    
    // ...
}
```

---

## 6. 阶段五：实时通信

### 6.1 WebSocket 钩子

**src/hooks/useWebSocket.ts:**
```ts
import { useEffect, useRef, useCallback } from 'react';
import { useAppStore } from '../stores/appStore';
import type { Message } from '../types';

export function useWebSocket(onMessage: (msg: Message) => void) {
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout>();
  const { setWsConnected } = useAppStore();
  
  const connect = useCallback(() => {
    const ws = new WebSocket('ws://localhost:8080/ws/admin');
    
    ws.onopen = () => {
      console.log('WebSocket connected');
      setWsConnected(true);
    };
    
    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        onMessage(msg);
      } catch (e) {
        console.error('Failed to parse WebSocket message:', e);
      }
    };
    
    ws.onclose = () => {
      console.log('WebSocket disconnected, reconnecting...');
      setWsConnected(false);
      reconnectTimeoutRef.current = setTimeout(connect, 3000);
    };
    
    ws.onerror = (error) => {
      console.error('WebSocket error:', error);
    };
    
    wsRef.current = ws;
  }, [onMessage, setWsConnected]);
  
  useEffect(() => {
    connect();
    
    return () => {
      clearTimeout(reconnectTimeoutRef.current);
      wsRef.current?.close();
    };
  }, [connect]);
  
  const send = useCallback((data: unknown) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(data));
    }
  }, []);
  
  return { send };
}
```

### 6.2 消息实时更新

在聊天室详情页集成:

```tsx
import { useWebSocket } from '../hooks/useWebSocket';
import { queryClient } from '../App';

export function RoomDetail() {
  const { id } = useParams<{ id: string }>();
  
  const handleMessage = useCallback((msg: Message) => {
    if (msg.room_id === id) {
      queryClient.invalidateQueries({ queryKey: ['messages', id] });
    }
  }, [id]);
  
  useWebSocket(handleMessage);
  // ...
}
```

---

## 7. 实施检查清单

### Phase 1 - 项目初始化
- [ ] 创建目录结构
- [ ] 配置 package.json
- [ ] 配置 Vite/TypeScript/Tailwind
- [ ] 创建入口文件

### Phase 2 - 核心组件
- [ ] 定义类型 (types/index.ts)
- [ ] 实现状态管理 (stores/appStore.ts)
- [ ] 实现 API 服务 (services/api.ts)
- [ ] 实现布局组件 (Layout, Sidebar, Header)

### Phase 3 - 页面开发
- [ ] 配置路由 (App.tsx)
- [ ] 实现 Dashboard 页面
- [ ] 实现 Agent 列表/详情页
- [ ] 实现聊天室列表/详情页
- [ ] 实现触发器列表/详情页
- [ ] 实现聊天组件

### Phase 4 - API 集成
- [ ] 实现 Coordinator 管理 API
- [ ] 注册路由
- [ ] 前端 API 调用集成

### Phase 5 - 实时通信
- [ ] 实现 WebSocket 钩子
- [ ] 集成消息实时更新
- [ ] 状态连接指示器
