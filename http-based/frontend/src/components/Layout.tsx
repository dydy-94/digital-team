import { Outlet, NavLink } from 'react-router-dom'
import {
  IconLayoutDashboard,
  IconMessageCircle,
  IconBolt,
  IconRobot
} from '@tabler/icons-react'

const navItems = [
  { path: '/', label: '仪表盘', icon: IconLayoutDashboard },
  { path: '/agents', label: 'Agent', icon: IconRobot },
  { path: '/rooms', label: '聊天室', icon: IconMessageCircle },
  { path: '/triggers', label: '触发器', icon: IconBolt }
]

export default function Layout() {
  return (
    <div className="flex h-screen">
      {/* 侧边栏 */}
      <aside className="w-56 bg-gray-900 text-white flex flex-col">
        <div className="p-4 border-b border-gray-700">
          <h1 className="text-lg font-bold">X-Client</h1>
          <p className="text-xs text-gray-400">管理后台</p>
        </div>
        <nav className="flex-1 p-2">
          {navItems.map((item) => (
            <NavLink
              key={item.path}
              to={item.path}
              end={item.path === '/'}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2 rounded-lg mb-1 transition-colors ${
                  isActive
                    ? 'bg-blue-600 text-white'
                    : 'text-gray-300 hover:bg-gray-800'
                }`
              }
            >
              <item.icon size={20} />
              <span>{item.label}</span>
            </NavLink>
          ))}
        </nav>
      </aside>

      {/* 主内容 */}
      <main className="flex-1 overflow-auto">
        <div className="p-6">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
