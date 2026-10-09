/**
 * 用量/额度进度条的公共口径：跑满为 error，≥80% 为 warning，其余 success；
 * 百分比统一钳到 [0, 100] 取整。密钥页、上游用量面板与余额看板共用——
 * 三处的阈值评论此前互相引用（「与上游用量面板一致」），现收敛到这一份。
 */
export type QuotaStatus = 'success' | 'warning' | 'error'

export function quotaPercent(used: number, limit: number): number {
  if (limit <= 0) return 0
  return Math.min(100, Math.max(0, Math.round((used / limit) * 100)))
}

export function quotaStatus(used: number, limit: number): QuotaStatus {
  if (limit <= 0) return 'success'
  if (used >= limit) return 'error'
  return used / limit >= 0.8 ? 'warning' : 'success'
}

/** 厂商返回的百分比字符串（如 "82.5"）版本的百分比；空/非法按 0。 */
export function quotaPercentFromString(percent: string | null | undefined): number {
  const p = parseFloat(percent ?? '')
  return Number.isNaN(p) ? 0 : Math.min(100, Math.max(0, Math.round(p)))
}

/** 厂商返回的百分比字符串版本的状态；空/非法按 success。 */
export function quotaStatusFromString(percent: string | null | undefined): QuotaStatus {
  const p = parseFloat(percent ?? '')
  if (Number.isNaN(p)) return 'success'
  if (p >= 100) return 'error'
  return p >= 80 ? 'warning' : 'success'
}
