const BASE_URL = '/api'

async function fetchJson<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options?.headers
    }
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: '请求失败' }))
    throw new Error(err.error || `HTTP ${res.status}`)
  }
  return res.json()
}

// ============ 聊天室 API ============

export interface Room {
  room_id: string
  name: string
  description: string
  created_by: string
  created_at: string
  member_count?: number
}

export async function getRooms(): Promise<Room[]> {
  const data = await fetchJson<{ success: boolean; rooms: Room[] }>(
    `${BASE_URL}/rooms`
  )
  return data.rooms || []
}

export async function getRoom(roomId: string): Promise<Room> {
  return fetchJson<Room>(`${BASE_URL}/room/${roomId}`)
}

export async function createRoom(params: {
  name: string
  description: string
  created_by: string
}): Promise<{ success: boolean; room_id: string }> {
  return fetchJson(`${BASE_URL}/room/create`, {
    method: 'POST',
    body: JSON.stringify(params)
  })
}

export async function deleteRoom(roomId: string): Promise<{ success: boolean }> {
  return fetchJson(`${BASE_URL}/room/${roomId}`, {
    method: 'DELETE'
  })
}

export async function getRoomMembers(
  roomId: string
): Promise<{ member_id: string; member_type: string; joined_at: number }[]> {
  const data = await fetchJson<{
    success: boolean
    members: { member_id: string; member_type: string; joined_at: number }[]
  }>(`${BASE_URL}/room/${roomId}/members`)
  return data.members || []
}

// ============ 触发器 API ============

export interface Trigger {
  id: string
  xclient_id: string
  name: string
  type: string
  config: Record<string, unknown>
  reason: string
  room_id: string
  room_valid: boolean
  status: string
  invalid_reason?: string
  fire_count: number
  cooldown_seconds: number
  created_at: number
  updated_at: number
}

export async function getTriggers(): Promise<Trigger[]> {
  const data = await fetchJson<{ success: boolean; triggers: Trigger[] }>(
    `${BASE_URL}/triggers`
  )
  return data.triggers || []
}

export async function getTrigger(triggerId: string): Promise<Trigger> {
  return fetchJson<Trigger>(`${BASE_URL}/trigger/${triggerId}`)
}

export async function createTrigger(params: {
  name: string
  type: string
  config: Record<string, unknown>
  reason: string
  room_id: string
  xclient_id: string
  cooldown_seconds?: number
}): Promise<{ success: boolean; trigger_id: string }> {
  return fetchJson(`${BASE_URL}/trigger`, {
    method: 'POST',
    body: JSON.stringify(params)
  })
}

export async function updateTrigger(
  triggerId: string,
  params: Partial<Trigger>
): Promise<{ success: boolean }> {
  return fetchJson(`${BASE_URL}/trigger/${triggerId}`, {
    method: 'PUT',
    body: JSON.stringify(params)
  })
}

export async function deleteTrigger(
  triggerId: string
): Promise<{ success: boolean }> {
  return fetchJson(`${BASE_URL}/trigger/${triggerId}`, {
    method: 'DELETE'
  })
}

export async function getTriggerExecutions(
  triggerId: string
): Promise<{ id: string; fired_at: number; fired_reason: string }[]> {
  const data = await fetchJson<{
    success: boolean
    executions: { id: string; fired_at: number; fired_reason: string }[]
  }>(`${BASE_URL}/trigger/${triggerId}/executions`)
  return data.executions || []
}

// ============ 统计 API ============

export async function getStats(): Promise<{
  room_count: number
  agent_count: number
  trigger_count: number
  enabled_trigger_count: number
}> {
  return fetchJson(`${BASE_URL}/stats`)
}

// ============ Agent API ============

export interface Agent {
  agent_id: string
  endpoint: string
  status: string
  last_heartbeat: string
  created_at: string
}

export async function getAgents(): Promise<Agent[]> {
  const data = await fetchJson<{ success: boolean; agents: Agent[] }>(
    `${BASE_URL}/agents`
  )
  return data.agents || []
}
