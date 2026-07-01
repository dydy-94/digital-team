import { useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getRoom, getRoomMembers } from '../api'
import type { Trigger } from '../api'

export default function RoomDetail() {
  const { roomId } = useParams<{ roomId: string }>()

  const { data: room, isLoading: roomLoading } = useQuery({
    queryKey: ['room', roomId],
    queryFn: () => getRoom(roomId!),
    enabled: !!roomId
  })

  const { data: members = [] } = useQuery({
    queryKey: ['roomMembers', roomId],
    queryFn: () => getRoomMembers(roomId!),
    enabled: !!roomId
  })

  const { data: triggers = [] } = useQuery({
    queryKey: ['triggers']
  })

  const roomTriggers = (triggers as Trigger[]).filter((t) => t.room_id === roomId)

  if (roomLoading) {
    return <div className="text-center py-8">加载中...</div>
  }

  if (!room) {
    return <div className="text-center py-8 text-red-600">聊天室不存在</div>
  }

  return (
    <div>
      <h2 className="text-2xl font-bold mb-6">{room.name}</h2>

      <div className="grid grid-cols-2 gap-6">
        {/* 基本信息 */}
        <div className="bg-white rounded-lg shadow p-4">
          <h3 className="font-semibold mb-4">基本信息</h3>
          <dl className="space-y-2">
            <div className="flex">
              <dt className="w-20 text-gray-500">ID</dt>
              <dd className="text-gray-900 font-mono text-sm">{room.room_id}</dd>
            </div>
            <div className="flex">
              <dt className="w-20 text-gray-500">描述</dt>
              <dd className="text-gray-900">{room.description || '-'}</dd>
            </div>
            <div className="flex">
              <dt className="w-20 text-gray-500">创建者</dt>
              <dd className="text-gray-900">{room.created_by}</dd>
            </div>
            <div className="flex">
              <dt className="w-20 text-gray-500">创建时间</dt>
              <dd className="text-gray-900">
                {new Date(room.created_at).toLocaleString()}
              </dd>
            </div>
          </dl>
        </div>

        {/* 成员列表 */}
        <div className="bg-white rounded-lg shadow p-4">
          <h3 className="font-semibold mb-4">
            成员 ({members.length})
          </h3>
          {members.length === 0 ? (
            <p className="text-gray-500 text-sm">暂无成员</p>
          ) : (
            <ul className="space-y-2">
              {members.map((member) => (
                <li
                  key={member.member_id}
                  className="flex items-center justify-between p-2 bg-gray-50 rounded"
                >
                  <span className="font-mono text-sm">{member.member_id}</span>
                  <span
                    className={`px-2 py-1 rounded text-xs ${
                      member.member_type === 'agent'
                        ? 'bg-blue-100 text-blue-700'
                        : 'bg-gray-100 text-gray-700'
                    }`}
                  >
                    {member.member_type}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* 关联触发器 */}
        <div className="col-span-2 bg-white rounded-lg shadow p-4">
          <h3 className="font-semibold mb-4">
            关联触发器 ({roomTriggers.length})
          </h3>
          {roomTriggers.length === 0 ? (
            <p className="text-gray-500 text-sm">暂无关联触发器</p>
          ) : (
            <table className="w-full">
              <thead className="bg-gray-50">
                <tr>
                  <th className="px-3 py-2 text-left text-sm font-medium text-gray-600">
                    名称
                  </th>
                  <th className="px-3 py-2 text-left text-sm font-medium text-gray-600">
                    类型
                  </th>
                  <th className="px-3 py-2 text-left text-sm font-medium text-gray-600">
                    触发原因
                  </th>
                  <th className="px-3 py-2 text-left text-sm font-medium text-gray-600">
                    状态
                  </th>
                  <th className="px-3 py-2 text-left text-sm font-medium text-gray-600">
                    触发次数
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {roomTriggers.map((trigger) => (
                  <tr key={trigger.id} className="hover:bg-gray-50">
                    <td className="px-3 py-2 font-medium">{trigger.name}</td>
                    <td className="px-3 py-2 text-gray-600">{trigger.type}</td>
                    <td className="px-3 py-2 text-gray-600">{trigger.reason}</td>
                    <td className="px-3 py-2">
                      <StatusBadge status={trigger.status} />
                    </td>
                    <td className="px-3 py-2">{trigger.fire_count}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
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
