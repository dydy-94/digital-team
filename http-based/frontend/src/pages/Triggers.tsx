import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { IconPlus, IconTrash } from '@tabler/icons-react'
import { getTriggers, getRooms, createTrigger, deleteTrigger } from '../api'

const TRIGGER_TYPES = [
  { value: 'cron', label: 'Cron 表达式' },
  { value: 'interval', label: '间隔触发' },
  { value: 'once', label: '单次触发' },
  { value: 'poll', label: '轮询触发' }
]

export default function Triggers() {
  const queryClient = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [formData, setFormData] = useState({
    name: '',
    type: 'interval',
    config: { minutes: 1 },
    reason: '',
    room_id: '',
    xclient_id: 'agent_001',
    cooldown_seconds: 0
  })

  const { data: triggers = [], isLoading } = useQuery({
    queryKey: ['triggers'],
    queryFn: getTriggers
  })

  const { data: rooms = [] } = useQuery({
    queryKey: ['rooms'],
    queryFn: getRooms
  })

  const createMutation = useMutation({
    mutationFn: createTrigger,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['triggers'] })
      setShowForm(false)
      setFormData({
        name: '',
        type: 'interval',
        config: { minutes: 1 },
        reason: '',
        room_id: '',
        xclient_id: 'agent_001',
        cooldown_seconds: 0
      })
    }
  })

  const deleteMutation = useMutation({
    mutationFn: deleteTrigger,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['triggers'] })
    }
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    createMutation.mutate(formData)
  }

  const handleConfigChange = (key: string, value: string | number) => {
    setFormData({
      ...formData,
      config: { ...formData.config, [key]: value }
    })
  }

  return (
    <div>
      <div className="flex justify-between items-center mb-6">
        <h2 className="text-2xl font-bold">触发器管理</h2>
        <button
          onClick={() => setShowForm(!showForm)}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700"
        >
          <IconPlus size={18} />
          创建触发器
        </button>
      </div>

      {/* 创建表单 */}
      {showForm && (
        <div className="bg-white rounded-lg shadow p-4 mb-6">
          <h3 className="font-semibold mb-4">新建触发器</h3>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium mb-1">名称</label>
                <input
                  type="text"
                  value={formData.name}
                  onChange={(e) =>
                    setFormData({ ...formData, name: e.target.value })
                  }
                  className="w-full px-3 py-2 border rounded-lg"
                  required
                />
              </div>
              <div>
                <label className="block text-sm font-medium mb-1">类型</label>
                <select
                  value={formData.type}
                  onChange={(e) =>
                    setFormData({ ...formData, type: e.target.value })
                  }
                  className="w-full px-3 py-2 border rounded-lg"
                >
                  {TRIGGER_TYPES.map((t) => (
                    <option key={t.value} value={t.value}>
                      {t.label}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium mb-1">
                  绑定聊天室
                </label>
                <select
                  value={formData.room_id}
                  onChange={(e) =>
                    setFormData({ ...formData, room_id: e.target.value })
                  }
                  className="w-full px-3 py-2 border rounded-lg"
                  required
                >
                  <option value="">选择聊天室</option>
                  {rooms.map((r) => (
                    <option key={r.room_id} value={r.room_id}>
                      {r.name}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium mb-1">
                  X-Client ID
                </label>
                <input
                  type="text"
                  value={formData.xclient_id}
                  onChange={(e) =>
                    setFormData({ ...formData, xclient_id: e.target.value })
                  }
                  className="w-full px-3 py-2 border rounded-lg"
                  required
                />
              </div>
              <div>
                <label className="block text-sm font-medium mb-1">
                  触发原因
                </label>
                <input
                  type="text"
                  value={formData.reason}
                  onChange={(e) =>
                    setFormData({ ...formData, reason: e.target.value })
                  }
                  className="w-full px-3 py-2 border rounded-lg"
                  placeholder="如：发送日报提醒"
                  required
                />
              </div>
              <div>
                <label className="block text-sm font-medium mb-1">
                  冷却时间（秒）
                </label>
                <input
                  type="number"
                  value={formData.cooldown_seconds}
                  onChange={(e) =>
                    setFormData({
                      ...formData,
                      cooldown_seconds: parseInt(e.target.value) || 0
                    })
                  }
                  className="w-full px-3 py-2 border rounded-lg"
                  min="0"
                />
              </div>
            </div>

            {/* 类型特定配置 */}
            {formData.type === 'interval' && (
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium mb-1">
                    间隔（分钟）
                  </label>
                  <input
                    type="number"
                    value={(formData.config as { minutes?: number }).minutes || 1}
                    onChange={(e) =>
                      handleConfigChange('minutes', parseInt(e.target.value) || 1)
                    }
                    className="w-full px-3 py-2 border rounded-lg"
                    min="1"
                    required
                  />
                </div>
              </div>
            )}

            {formData.type === 'cron' && (
              <div>
                <label className="block text-sm font-medium mb-1">
                  Cron 表达式
                </label>
                <input
                  type="text"
                  value={(formData.config as { expr?: string }).expr || ''}
                  onChange={(e) => handleConfigChange('expr', e.target.value)}
                  className="w-full px-3 py-2 border rounded-lg"
                  placeholder="0 9 * * 1-5"
                  required
                />
              </div>
            )}

            {formData.type === 'once' && (
              <div>
                <label className="block text-sm font-medium mb-1">
                  执行时间（时间戳，毫秒）
                </label>
                <input
                  type="number"
                  value={(formData.config as { timestamp?: number }).timestamp || ''}
                  onChange={(e) =>
                    handleConfigChange('timestamp', parseInt(e.target.value))
                  }
                  className="w-full px-3 py-2 border rounded-lg"
                  placeholder="1749408000000"
                  required
                />
              </div>
            )}

            {formData.type === 'poll' && (
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium mb-1">
                    轮询 URL
                  </label>
                  <input
                    type="text"
                    value={(formData.config as { url?: string }).url || ''}
                    onChange={(e) => handleConfigChange('url', e.target.value)}
                    className="w-full px-3 py-2 border rounded-lg"
                    placeholder="https://api.example.com/status"
                    required
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1">
                    轮询间隔（分钟）
                  </label>
                  <input
                    type="number"
                    value={(formData.config as { interval?: number }).interval || 5}
                    onChange={(e) =>
                      handleConfigChange('interval', parseInt(e.target.value) || 5)
                    }
                    className="w-full px-3 py-2 border rounded-lg"
                    min="1"
                    required
                  />
                </div>
              </div>
            )}

            <div className="flex gap-2">
              <button
                type="submit"
                disabled={createMutation.isPending}
                className="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50"
              >
                {createMutation.isPending ? '创建中...' : '创建'}
              </button>
              <button
                type="button"
                onClick={() => setShowForm(false)}
                className="px-4 py-2 border rounded-lg hover:bg-gray-50"
              >
                取消
              </button>
            </div>
          </form>
        </div>
      )}

      {/* 触发器列表 */}
      {isLoading ? (
        <div className="text-center py-8 text-gray-500">加载中...</div>
      ) : triggers.length === 0 ? (
        <div className="text-center py-8 text-gray-500">暂无触发器</div>
      ) : (
        <div className="bg-white rounded-lg shadow overflow-hidden">
          <table className="w-full">
            <thead className="bg-gray-50">
              <tr>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  名称
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  类型
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  聊天室
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  触发原因
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  状态
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  触发次数
                </th>
                <th className="px-4 py-3 text-right text-sm font-medium text-gray-600">
                  操作
                </th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {triggers.map((trigger) => (
                <tr key={trigger.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3 font-medium">{trigger.name}</td>
                  <td className="px-4 py-3 text-gray-600">
                    {TRIGGER_TYPES.find((t) => t.value === trigger.type)
                      ?.label || trigger.type}
                  </td>
                  <td className="px-4 py-3 text-gray-600">
                    {trigger.room_id}
                  </td>
                  <td className="px-4 py-3 text-gray-600">{trigger.reason}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-2">
                      <StatusBadge status={trigger.status} />
                      {!trigger.room_valid && (
                        <span className="text-xs text-red-500">
                          聊天室已删除
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3">{trigger.fire_count}</td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => {
                        if (confirm('确定删除触发器？'))
                          deleteMutation.mutate(trigger.id)
                      }}
                      disabled={deleteMutation.isPending}
                      className="p-1 text-gray-600 hover:text-red-600"
                      title="删除"
                    >
                      <IconTrash size={18} />
                    </button>
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
