import client from './client'

export interface ConversationListItem {
  id: number; ext_key_id: number | null; upstream_id: number | null
  upstream_name: string; model: string; in_format: string; up_format: string
  harness: string; user_agent: string; stream: boolean; status: string
  prompt_tokens: number; completion_tokens: number; total_tokens: number
  cache_read_tokens: number; cache_creation_tokens: number; reasoning_tokens: number
  created_at: string
}

export interface ConversationDetail extends ConversationListItem {
  request_ir: string; response_ir: string
}

export async function fetchConversations(page: number, size: number) {
  const { data } = await client.get('/conversations', { params: { page, size } })
  return data as { data: ConversationListItem[]; total: number; disabled?: boolean }
}

export async function fetchConversation(id: number) {
  const { data } = await client.get(`/conversations/${id}`)
  return data.data as ConversationDetail
}

/* ---- 会话聚合视图（conversation_sessions，按会话一行） ---- */

export interface ConvSessionListItem {
  id: number; session_id: string; ext_key_id: number | null
  harness: string; in_format: string; model: string
  turn_count: number
  prompt_tokens: number; completion_tokens: number; total_tokens: number
  cache_read_tokens: number; cache_creation_tokens: number; reasoning_tokens: number
  status: string; msg_count: number
  created_at: string; last_active_at: string
}

export interface ConvSessionDetail extends ConvSessionListItem {
  messages: string
}

export async function fetchConvSessions(page: number, size: number) {
  const { data } = await client.get('/conv-sessions', { params: { page, size } })
  return data as { data: ConvSessionListItem[]; total: number; disabled?: boolean }
}

export async function fetchConvSession(id: number) {
  const { data } = await client.get(`/conv-sessions/${id}`)
  return data.data as ConvSessionDetail
}
