import client from './client'

export interface ExtKey {
  id: number; key: string; label: string; enabled: boolean;
  daily_token_limit: number; monthly_token_limit: number;
  created_at?: string
}

export async function listKeys() {
  const { data } = await client.get('/keys')
  return data.data as ExtKey[]
}

export async function createKey(label: string, dailyTokenLimit = 0, monthlyTokenLimit = 0) {
  const { data } = await client.post('/keys', {
    label,
    daily_token_limit: dailyTokenLimit,
    monthly_token_limit: monthlyTokenLimit,
  })
  return data as ExtKey
}

export async function updateKey(id: number, patch: Partial<Pick<ExtKey, 'label' | 'enabled' | 'daily_token_limit' | 'monthly_token_limit'>>) {
  const { data } = await client.put(`/keys/${id}`, patch)
  return data as ExtKey
}

export async function deleteKey(id: number) {
  await client.delete(`/keys/${id}`)
}

export interface UsageTotals { daily_tokens: number; monthly_tokens: number }

export async function getKeyUsage(id: number) {
  const { data } = await client.get(`/usage/key/${id}`)
  return data as UsageTotals
}
