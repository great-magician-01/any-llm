import type { ConnectivityTestResult } from '@/api/upstreams'

// 连通性测试结果的展示形态：message / n-alert 都按这三个级别渲染。
export interface ConnectivityView {
  type: 'success' | 'warning' | 'error'
  text: string
}

// 把后端的三层结果（网络失败 / 非 2xx 应答 / 2xx 正常）翻成一句人话，
// classic 与 glass 两套上游页面共用，保证两处口径一致。
export function connectivityView(r: ConnectivityTestResult): ConnectivityView {
  const latency = ` · ${r.latency_ms}ms`
  if (r.ok) {
    const models = r.models != null ? `，列出 ${r.models} 个模型` : ''
    return { type: 'success', text: `连通正常${models}${latency}` }
  }
  if (!r.reachable) {
    return { type: 'error', text: `无法连通：${r.detail || '网络错误'}${latency}` }
  }
  if (r.status === 401 || r.status === 403) {
    return { type: 'error', text: `已连通但认证失败（HTTP ${r.status}），请检查 API Key${latency}` }
  }
  // 其它非 2xx：能连通但端点不符合预期——如部分 anthropic 兼容端点没有
  // /models（404），或 2xx 但应答不是模型列表（status 为 200 但 ok=false）。
  if (r.status != null && r.status >= 200 && r.status < 300) {
    return { type: 'warning', text: `可连通，但应答不是模型列表（该上游的 /models 可能不规范）${latency}` }
  }
  return { type: 'warning', text: `可连通，但 /models 返回 HTTP ${r.status ?? '未知'}（该上游可能不支持模型列表接口）${latency}` }
}
