import client from './client'

export interface ExtKey {
  id: number; key: string; label: string; enabled: boolean; created_at?: string
}

export async function listKeys() {
  const { data } = await client.get('/keys')
  return data.data as ExtKey[]
}

export async function createKey(label: string) {
  const { data } = await client.post('/keys', { label })
  return data as ExtKey
}

export async function deleteKey(id: number) {
  await client.delete(`/keys/${id}`)
}
