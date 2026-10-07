/**
 * naive-ui useMessage 的替身：把页面弹出的提示按 (level, text) 记下来，
 * 让测试直接断言「用户看到的那句话」，而不必去 DOM 里捞 message 浮层。
 *
 * 用法（测试文件顶部，vi.mock 会被提升到 import 之前）：
 *   vi.mock('naive-ui', async (importOriginal) => {
 *     const actual = await importOriginal<typeof import('naive-ui')>()
 *     const { messageApi } = await import('@/test/naiveMessage')
 *     return { ...actual, useMessage: () => messageApi }
 *   })
 *
 * 只替换 useMessage，其余 naive-ui 导出保持原样——组件照常真实渲染。
 */

export type MessageLevel = 'success' | 'warning' | 'error' | 'info'

export interface MessageCall {
  level: MessageLevel
  text: string
}

export const messageCalls: MessageCall[] = []

function record(level: MessageLevel) {
  // 真实签名是 (text, options?)，option 对断言无意义
  return (text: string) => {
    messageCalls.push({ level, text })
  }
}

export const messageApi = {
  success: record('success'),
  warning: record('warning'),
  error: record('error'),
  info: record('info'),
}

export function resetMessages(): void {
  messageCalls.length = 0
}

/** 最后一条提示的文本（没有则空串） */
export function lastMessage(): string {
  return messageCalls.length ? messageCalls[messageCalls.length - 1].text : ''
}

export function messagesOf(level: MessageLevel): string[] {
  return messageCalls.filter((c) => c.level === level).map((c) => c.text)
}
