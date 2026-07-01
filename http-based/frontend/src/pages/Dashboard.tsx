import { useQuery } from '@tanstack/react-query'
import { getRooms, getTriggers } from '../api'

export default function Dashboard() {
  const { data: rooms = [] } = useQuery({
    queryKey: ['rooms'],
    queryFn: getRooms
  })

  const { data: triggers = [] } = useQuery({
    queryKey: ['triggers'],
    queryFn: getTriggers
  })

  const enabledTriggers = triggers.filter((t) => t.status === 'enabled')
  const invalidTriggers = triggers.filter((t) => t.status === 'invalid')

  return (
    <div>
      <h2 className="text-2xl font-bold mb-6">仪表盘</h2>

      {/* 统计卡片 */}
      <div className="grid grid-cols-4 gap-4 mb-8">
        <StatCard label="聊天室" value={rooms.length} color="blue" />
        <StatCard label="活跃触发器" value={enabledTriggers.length} color="green" />
        <StatCard label="已失效触发器" value={invalidTriggers.length} color="red" />
        <StatCard label="总触发器" value={triggers.length} color="gray" />
      </div>

      {/* 最近活动 */}
      <div className="grid grid-cols-2 gap-6">
        {/* 聊天室列表 */}
        <div className="bg-white rounded-lg shadow p-4">
          <h3 className="font-semibold mb-4">聊天室</h3>
          {rooms.length === 0 ? (
            <p className="text-gray-500 text-sm">暂无聊天室</p>
          ) : (
            <ul className="space-y-2">
              {rooms.slice(0, 5).map((room) => (
                <li
                  key={room.room_id}
                  className="flex justify-between items-center p-2 hover:bg-gray-50 rounded"
                >
                  <div>
                    <div className="font-medium">{room.name}</div>
                    <div className="text-xs text-gray-500">{room.description}</div>
                  </div>
                  <div className="text-xs text-gray-400">
                    {room.member_count} 成员
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* 触发器列表 */}
        <div className="bg-white rounded-lg shadow p-4">
          <h3 className="font-semibold mb-4">触发器</h3>
          {triggers.length === 0 ? (
            <p className="text-gray-500 text-sm">暂无触发器</p>
          ) : (
            <ul className="space-y-2">
              {triggers.slice(0, 5).map((trigger) => (
                <li
                  key={trigger.id}
                  className="flex justify-between items-center p-2 hover:bg-gray-50 rounded"
                >
                  <div>
                    <div className="font-medium">{trigger.name}</div>
                    <div className="text-xs text-gray-500">
                      {trigger.type} · {trigger.reason}
                    </div>
                  </div>
                  <StatusBadge status={trigger.status} />
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  )
}

function StatCard({
  label,
  value,
  color
}: {
  label: string
  value: number
  color: 'blue' | 'green' | 'red' | 'gray'
}) {
  const colors = {
    blue: 'bg-blue-500',
    green: 'bg-green-500',
    red: 'bg-red-500',
    gray: 'bg-gray-500'
  }
  return (
    <div className="bg-white rounded-lg shadow p-4">
      <div className={`w-2 h-10 ${colors[color]} rounded-full mb-2`} />
      <div className="text-3xl font-bold">{value}</div>
      <div className="text-gray-500 text-sm">{label}</div>
    </div>
  )
}

function StatusBadge({ status }: { status: string }) {
  const styles = {
    enabled: 'bg-green-100 text-green-700',
    invalid: 'bg-red-100 text-red-700',
    paused: 'bg-yellow-100 text-yellow-700'
  }
  return (
    <span
      className={`px-2 py-1 rounded text-xs font-medium ${
        styles[status as keyof typeof styles] || 'bg-gray-100 text-gray-700'
      }`}
    >
      {status === 'enabled' ? '启用' : status === 'invalid' ? '失效' : '暂停'}
    </span>
  )
}
