import { useRoute, useRouter } from 'vue-router'

/**
 * 登录成功后该回哪一页。
 *
 * 路由守卫在把人弹去登录页时，会把原目标写进 `?redirect=`（深链、会话过期回来
 * 都靠它），登录成功后跳回去；拿不到就回主题首页。
 *
 * `safeRedirect` 是开放重定向的闸门：只接受**站内绝对路径**。
 * `redirect` 来自 URL，谁都能构造 `#/login?redirect=//evil.com` —— `//host` 是
 * 协议相对 URL，浏览器会跳去外站；反斜杠变体（`/\evil.com`）在部分浏览器里
 * 等价于 `//`。这两种一律拒绝，回落到首页。
 */
export function safeRedirect(raw: unknown): string | null {
  if (typeof raw !== 'string' || raw === '') return null
  if (!raw.startsWith('/')) return null
  if (raw.startsWith('//')) return null
  if (raw.includes('\\')) return null
  return raw
}

/** 返回一个「登录成功后执行跳转」的函数；fallback 是该主题的默认落地页 */
export function useLoginRedirect(fallback: string): () => Promise<unknown> {
  const route = useRoute()
  const router = useRouter()
  return () => router.push(safeRedirect(route.query.redirect) ?? fallback)
}
