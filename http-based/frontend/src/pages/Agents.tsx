import { useQuery } from '@tanstack/react-query'
import { getAgents } from '../api'

export default function Agents() {
  const { data: agents = [], isLoading: agentsLoading } = useQuery({
    queryKey: ['agents'],
    queryFn: getAgents
  })

  return (
    <div>
      <h2 className="text-2xl font-bold mb-6">Agent 管理</h2>

      {/* 统计卡片 */}
      <div className="grid grid-cols-3 gap-4 mb-6">
        <div className="bg-white rounded-lg shadow p-4">
          <div className="text-3xl font-bold text-blue-600">{agents.length}</div>
          <div className="text-gray-500">总 Agent 数</div>
        </div>
        <div className="bg-white rounded-lg shadow p-4">
          <div className="text-3xl font-bold text-green-600">
            {agents.filter((a) => a.status === 'ONLINE').length}
          </div>
          <div className="text-gray-500">在线</div>
        </div>
        <div className="bg-white rounded-lg shadow p-4">
          <div className="text-3xl font-bold text-gray-400">
            {agents.filter((a) => a.status !== 'ONLINE').length}
          </div>
          <div className="text-gray-500">离线</div>
        </div>
      </div>

      {/* Agent 列表 */}
      {agentsLoading ? (
        <div className="text-center py-8 text-gray-500">加载中...</div>
      ) : agents.length === 0 ? (
        <div className="text-center py-8 text-gray-500">暂无 Agent</div>
      ) : (
        <div className="bg-white rounded-lg shadow overflow-hidden">
          <table className="w-full">
            <thead className="bg-gray-50">
              <tr>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  Agent ID
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  Endpoint
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  状态
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  最后心跳
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  注册时间
                </th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {agents.map((agent) => (
                <tr key={agent.agent_id} className="hover:bg-gray-50">
                  <td className="px-4 py-3 font-mono text-sm">{agent.agent_id}</td>
                  <td className="px-4 py-3 text-gray-600 text-sm">{agent.endpoint}</td>
                  <td className="px-4 py-3">
                    <StatusBadge status={agent.status} />
                  </td>
                  <td className="px-4 py-3 text-gray-600 text-sm">
                    {agent.last_heartbeat
                      ? new Date(agent.last_heartbeat).toLocaleString()
                      : '-'}
                  </td>
                  <td className="px-4 py-3 text-gray-600 text-sm">
                    {agent.created_at
                      ? new Date(agent.created_at).toLocaleString()
                      : '-'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function StatusBadge({ status }: { status: string }) {
  const styles = {
    ONLINE: 'bg-green-100 text-green-700',
    OFFLINE: 'bg-gray-100 text-gray-700'
  }
  return (
    <span
      className={`px-2 py-1 rounded text-xs font-medium ${
        styles[status as keyof typeof styles] || 'bg-gray-100 text-gray-700'
      }`}
    >
      {status === 'ONLINE' ? '在线' : status === 'OFFLINE' ? '离线' : status}
    </span>
  )
}
