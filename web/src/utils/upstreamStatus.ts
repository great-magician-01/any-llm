import type { Upstream } from '../api/upstreams'
import { formatTime, localISO } from './format'

/**
 * 上游有效期（expires_at）的判定与展示。
 *
 * 语义与后端 model.Upstream.Expired 一致：到点即失效（含等于截止时刻），
 * 未设置（null）表示永久有效。两端各判一次——后端决定网关是否放行，这里只
 * 决定管理端怎么显示，以及别名下拉要不要禁用某个上游。
 */

/** 已过有效期。无效日期串按「未过期」处理，格式把关交给后端。 */
export function isExpired(u: Upstream, now: number = Date.now()): boolean {
  if (!u.expires_at) return false
  const t = new Date(u.expires_at).getTime()
  if (isNaN(t)) return false
  return t <= now
}

/** 截止时刻；未设置为 null。 */
export function expiryTime(u: Upstream): number | null {
  if (!u.expires_at) return null
  const t = new Date(u.expires_at).getTime()
  return isNaN(t) ? null : t
}

export type ExpiryTone = 'ok' | 'error' | 'muted'

/**
 * 「有效期至」列的展示态。tone 供调用方决定颜色：
 *   ok    未到期，显示具体时间
 *   error 已到期，标红
 *   muted 永久有效，弱化显示
 */
export function expiryLabel(u: Upstream, now: number = Date.now()): { text: string; tone: ExpiryTone } {
  if (isExpired(u, now)) return { text: '已过期', tone: 'error' }
  const t = expiryTime(u)
  if (t == null) return { text: '永久', tone: 'muted' }
  return { text: formatTime(u.expires_at as string), tone: 'ok' }
}

/**
 * n-date-picker 默认的 v-model 是 epoch ms（见 Usage.vue 的既有用法），
 * API 要的是 ISO 字符串；两侧在这里转换，复用仓库里已验证的 localISO。
 * 不采用 date-picker 的 value-format：那要求格式串与后端输出精确匹配，
 * 而后端时间戳可能带时区偏移甚至亚秒，容易静默失配。
 */
export function expiryToISO(ms: number | null): string | null {
  return ms == null ? null : localISO(new Date(ms))
}

export function isoToExpiry(iso: string | null | undefined): number | null {
  return expiryTime({ expires_at: iso } as Upstream)
}
