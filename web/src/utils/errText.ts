/**
 * 从捕获到的异常里提取可展示的文案：优先服务端返回的错误体
 * （axios 把网关的 JSON error 放在 e.response.data.error），其次 Error.message，
 * 最后原样字符串化。入参是 unknown，调用方无需再写 `catch (e: any)`。
 *
 * 此前各视图手写同一表达式（有的只取 e.message，丢掉服务端明细），
 * 统一到这里保证所有页面展示同一种错误信息。
 */
export function errText(e: unknown): string {
  const r = (e as { response?: { data?: unknown } } | null | undefined)?.response
  const data = r?.data
  if (data != null) {
    if (typeof data === 'string') return data
    if (typeof data === 'object' && 'error' in data) {
      return String((data as { error: unknown }).error)
    }
    return JSON.stringify(data)
  }
  const msg = (e as { message?: unknown } | null | undefined)?.message
  if (typeof msg === 'string' && msg !== '') return msg
  return String(e)
}
