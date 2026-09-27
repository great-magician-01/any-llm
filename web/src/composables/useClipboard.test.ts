/**
 * useClipboard 的行为契约。
 *
 * 守的规则（两套 Keys 页面 + 使用文档抽屉共用这一份）：
 *   - 优先 navigator.clipboard.writeText（HTTPS/localhost 才有），失败才退回 execCommand；
 *   - 回退路径里的临时 textarea 必须挂在「被点击按钮所在容器」里（naive-ui 弹窗的焦点陷阱
 *     会把挂在 body 上的 textarea 抢走焦点并清空选区），并且用完必须摘掉；
 *   - 回退成功与否只认 copy 事件是否真的拿到了数据（execCommand 的返回值不算数），
 *     失败时给用户「请手动选择复制」，不能假装成功。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useClipboard } from './useClipboard'
import { lastMessage, messageCalls, resetMessages } from '@/test/naiveMessage'

vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
const execDescriptor = Object.getOwnPropertyDescriptor(document, 'execCommand')

function setClipboard(value: unknown): void {
  Object.defineProperty(navigator, 'clipboard', { value, configurable: true, writable: true })
}

/** 模拟浏览器的 copy 事件：execCommand('copy') 会同步触发它，handler 里 setData 才算成功 */
function simulateCopyEvent(setData: (type: string, data: string) => void): void {
  const event = new Event('copy') as Event & { clipboardData: unknown }
  event.clipboardData = { setData }
  document.dispatchEvent(event)
}

/** 放一个「弹窗内的按钮」：回退路径应当把 textarea 塞进它的父容器 */
function anchorInside(container: HTMLElement): MouseEvent {
  const button = document.createElement('button')
  container.appendChild(button)
  return { currentTarget: button } as unknown as MouseEvent
}

beforeEach(() => {
  resetMessages()
  setClipboard(undefined)
  Object.defineProperty(document, 'execCommand', { value: undefined, configurable: true, writable: true })
})

afterEach(() => {
  if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor)
  else delete (navigator as any).clipboard
  if (execDescriptor) Object.defineProperty(document, 'execCommand', execDescriptor)
  else delete (document as any).execCommand
  document.body.innerHTML = ''
})

describe('useClipboard', () => {
  it('navigator.clipboard 可用时直接写剪贴板，不走 execCommand', async () => {
    const writeText = vi.fn(async () => {})
    setClipboard({ writeText })
    const exec = vi.fn(() => true)
    Object.defineProperty(document, 'execCommand', { value: exec, configurable: true, writable: true })

    await useClipboard().copyText('all-sk-xxx')

    expect(writeText).toHaveBeenCalledWith('all-sk-xxx')
    expect(exec).not.toHaveBeenCalled()
    expect(lastMessage()).toBe('已复制到剪贴板')
    expect(messageCalls).toHaveLength(1)
  })

  // 规则：clipboard 不存在（HTTP 非 localhost）时必须退回 execCommand，且临时节点
  // 挂在被点击按钮的容器里、用完立刻摘掉
  it('剪贴板 API 不可用时退回 execCommand，临时 textarea 挂在按钮容器内并在结束后移除', async () => {
    setClipboard(undefined)
    const container = document.createElement('div')
    document.body.appendChild(container)

    const setData = vi.fn()
    let parentDuringCopy: HTMLElement | null = null
    let valueDuringCopy = ''
    const exec = vi.fn(() => {
      const ta = document.querySelector('textarea') as HTMLTextAreaElement | null
      parentDuringCopy = ta?.parentElement ?? null
      valueDuringCopy = ta?.value ?? ''
      simulateCopyEvent(setData)
      return true
    })
    Object.defineProperty(document, 'execCommand', { value: exec, configurable: true, writable: true })

    await useClipboard().copyText('secret', anchorInside(container))

    expect(exec).toHaveBeenCalledWith('copy')
    // 关键：payload 由 copy 事件强制写入，不依赖浏览器有没有保住选区
    expect(setData).toHaveBeenCalledWith('text/plain', 'secret')
    expect(parentDuringCopy).toBe(container)
    expect(valueDuringCopy).toBe('secret')
    expect(document.querySelector('textarea')).toBeNull()
    expect(lastMessage()).toBe('已复制到剪贴板')
  })

  // 规则：execCommand 返回 true 但没真拿到数据（没触发 copy 事件）时不能谎报成功
  it('回退路径拿不到 copy 事件时提示手动复制', async () => {
    const exec = vi.fn(() => true)
    Object.defineProperty(document, 'execCommand', { value: exec, configurable: true, writable: true })

    await useClipboard().copyText('secret')

    expect(exec).toHaveBeenCalled()
    expect(lastMessage()).toBe('复制失败，请手动选择复制')
    expect(document.querySelector('textarea')).toBeNull()
  })

  // 规则：execCommand 直接抛错 / 不实现也要兜住，给同一句失败提示
  it('execCommand 抛错时降级为手动复制提示', async () => {
    const exec = vi.fn(() => { throw new Error('not implemented') })
    Object.defineProperty(document, 'execCommand', { value: exec, configurable: true, writable: true })

    await useClipboard().copyText('secret')

    expect(lastMessage()).toBe('复制失败，请手动选择复制')
    expect(document.querySelector('textarea')).toBeNull()
  })

  // 规则：writeText 被拒绝（非安全上下文/权限被拒）要静默转回退路径，只给一条提示
  it('writeText 被拒绝时转 execCommand 回退，只弹一条成功提示', async () => {
    const writeText = vi.fn(async () => { throw new Error('NotAllowedError') })
    setClipboard({ writeText })
    const setData = vi.fn()
    Object.defineProperty(document, 'execCommand', {
      value: () => { simulateCopyEvent(setData); return true },
      configurable: true,
      writable: true,
    })

    await useClipboard().copyText('secret')

    expect(writeText).toHaveBeenCalledWith('secret')
    expect(setData).toHaveBeenCalledWith('text/plain', 'secret')
    expect(messageCalls).toEqual([{ level: 'success', text: '已复制到剪贴板' }])
  })

  // 规则：拿不到点击事件时（键盘触发等）退到 document.activeElement 作锚点，功能不能失效
  it('没有事件对象时用 document.activeElement 作为锚点，仍能完成复制', async () => {
    const setData = vi.fn()
    Object.defineProperty(document, 'execCommand', {
      value: () => { simulateCopyEvent(setData); return true },
      configurable: true,
      writable: true,
    })

    await useClipboard().copyText('plain')

    expect(setData).toHaveBeenCalledWith('text/plain', 'plain')
    expect(lastMessage()).toBe('已复制到剪贴板')
    expect(document.querySelector('textarea')).toBeNull()
  })
})
