import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { IconPlus, IconTrash, IconExternalLink } from '@tabler/icons-react'
import { getRooms, createRoom, deleteRoom } from '../api'

export default function Rooms() {
  const queryClient = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [formData, setFormData] = useState({
    name: '',
    description: '',
    created_by: 'admin'
  })

  const { data: rooms = [], isLoading } = useQuery({
    queryKey: ['rooms'],
    queryFn: getRooms
  })

  const createMutation = useMutation({
    mutationFn: createRoom,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['rooms'] })
      setShowForm(false)
      setFormData({ name: '', description: '', created_by: 'admin' })
    }
  })

  const deleteMutation = useMutation({
    mutationFn: deleteRoom,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['rooms'] })
    }
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    createMutation.mutate(formData)
  }

  return (
    <div>
      <div className="flex justify-between items-center mb-6">
        <h2 className="text-2xl font-bold">聊天室管理</h2>
        <button
          onClick={() => setShowForm(!showForm)}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700"
        >
          <IconPlus size={18} />
          创建聊天室
        </button>
      </div>

      {/* 创建表单 */}
      {showForm && (
        <div className="bg-white rounded-lg shadow p-4 mb-6">
          <h3 className="font-semibold mb-4">新建聊天室</h3>
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
                <label className="block text-sm font-medium mb-1">描述</label>
                <input
                  type="text"
                  value={formData.description}
                  onChange={(e) =>
                    setFormData({ ...formData, description: e.target.value })
                  }
                  className="w-full px-3 py-2 border rounded-lg"
                />
              </div>
            </div>
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

      {/* 聊天室列表 */}
      {isLoading ? (
        <div className="text-center py-8 text-gray-500">加载中...</div>
      ) : rooms.length === 0 ? (
        <div className="text-center py-8 text-gray-500">暂无聊天室</div>
      ) : (
        <div className="bg-white rounded-lg shadow overflow-hidden">
          <table className="w-full">
            <thead className="bg-gray-50">
              <tr>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  名称
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  描述
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  成员数
                </th>
                <th className="px-4 py-3 text-left text-sm font-medium text-gray-600">
                  创建者
                </th>
                <th className="px-4 py-3 text-right text-sm font-medium text-gray-600">
                  操作
                </th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {rooms.map((room) => (
                <tr key={room.room_id} className="hover:bg-gray-50">
                  <td className="px-4 py-3">
                    <Link
                      to={`/rooms/${room.room_id}`}
                      className="text-blue-600 hover:underline font-medium"
                    >
                      {room.name}
                    </Link>
                  </td>
                  <td className="px-4 py-3 text-gray-600">{room.description}</td>
                  <td className="px-4 py-3">{room.member_count || 0}</td>
                  <td className="px-4 py-3 text-gray-600">{room.created_by}</td>
                  <td className="px-4 py-3 text-right">
                    <div className="flex justify-end gap-2">
                      <Link
                        to={`/rooms/${room.room_id}`}
                        className="p-1 text-gray-600 hover:text-blue-600"
                        title="查看详情"
                      >
                        <IconExternalLink size={18} />
                      </Link>
                      <button
                        onClick={() => {
                          if (confirm('确定删除聊天室？'))
                            deleteMutation.mutate(room.room_id)
                        }}
                        disabled={deleteMutation.isPending}
                        className="p-1 text-gray-600 hover:text-red-600"
                        title="删除"
                      >
                        <IconTrash size={18} />
                      </button>
                    </div>
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
