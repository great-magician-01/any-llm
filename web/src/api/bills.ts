import client from './client'

/** 账单：余额快照联动的自动记账（kind=auto）+ 管理端手动记账（kind=manual） */
export interface Bill {
  id: number
  upstream_id: number
  upstream_name: string
  kind: 'auto' | 'manual'
  /** 金额，1e-6 币种单位的整数（后端 SUM 是精确整数运算，展示时除回来） */
  amount_micros: number
  currency: string
  billed_at: string
  note: string
  /** 仅 auto：推导依据 JSON（prev/new 余额、关联快照 id） */
  detail?: string
  created_at: string
}

/** 一个时间桶（日/月/年）的趋势聚合 */
export interface BillBucketStat {
  bucket: string
  amount_micros: number
  count: number
}

/** 时间窗口内一个上游（+币种）的合计 */
export interface BillUpstreamTotal {
  upstream_id: number
  upstream_name: string
  currency: string
  amount_micros: number
  count: number
}

/** 窗口内一个记账来源（auto/manual）的合计 */
export interface BillKindTotal {
  kind: Bill['kind']
  count: number
  amount_micros: number
}

export interface BillSummary {
  buckets: BillBucketStat[]
  upstreams: BillUpstreamTotal[]
  kinds: BillKindTotal[]
}

/** 手动记账请求体：金额用十进制字符串传递，避免浮点误差进账本 */
export interface BillInput {
  upstream_id: number
  date: string
  amount: string
  currency?: string
  note?: string
}

export type BillBucket = 'day' | 'month' | 'year'

export interface BillListParams {
  upstream_id?: number
  from?: string
  to?: string
  page: number
  size: number
}

export async function fetchBills(params: BillListParams) {
  const { data } = await client.get('/bills', { params })
  return data as { data: Bill[]; total: number }
}

export async function fetchBillSummary(params: { bucket: BillBucket; upstream_id?: number; from?: string; to?: string }) {
  const { data } = await client.get('/bills/summary', { params })
  return data as BillSummary
}

export async function createBill(input: BillInput) {
  const { data } = await client.post('/bills', input)
  return data.data as Bill
}

export async function updateBill(id: number, input: BillInput) {
  const { data } = await client.put(`/bills/${id}`, input)
  return data.data as Bill
}

export async function deleteBill(id: number) {
  await client.delete(`/bills/${id}`)
}
