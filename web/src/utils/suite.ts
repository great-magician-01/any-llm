/**
 * 经典版与液态玻璃版路由一一对应：/x ↔ /glass/x（配对关系由
 * router.navigation.test.ts 保障）。切换套件时跳到当前页面在对侧的
 * 对应页面而不是对侧首页，并保留 query——登录页互切时连 ?redirect=
 * 深链一起翻译过去，登录后仍落回本套件的目标页。
 */
export function switchSuite(fullPath: string): string {
  const qIndex = fullPath.indexOf('?')
  const path = qIndex === -1 ? fullPath : fullPath.slice(0, qIndex)
  const query = qIndex === -1 ? '' : fullPath.slice(qIndex + 1)
  const target = counterpartPath(path)
  if (!query) return target
  const params = new URLSearchParams(query)
  const redirect = params.get('redirect')
  // 只翻译站内路径（与登录守卫/登录落地校验同源，绝不外跳）
  if (redirect?.startsWith('/')) {
    params.set('redirect', counterpartPath(redirect))
  }
  const next = params.toString()
  return next ? `${target}?${next}` : target
}

function counterpartPath(path: string): string {
  if (path.startsWith('/glass/')) return path.slice('/glass'.length)
  if (path === '/glass') return '/'
  return `/glass${path}`
}
