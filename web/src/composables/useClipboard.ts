import { useMessage } from 'naive-ui'

/**
 * 复制文本到剪贴板并弹出反馈。优先 navigator.clipboard（仅 HTTPS / localhost 可用），
 * 失败时退化到 execCommand。两个 Keys 皮肤和使用文档抽屉共用这一份。
 */
export function useClipboard() {
  const message = useMessage()

  async function copyText(text: string, evt?: MouseEvent) {
    // prefer the modern async clipboard API (HTTPS / localhost only)
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(text)
        message.success('已复制到剪贴板')
        return
      } catch {
        // permission denied or non-secure context — fall through
      }
    }
    // legacy fallback for HTTP non-localhost:
    // the temp textarea must live INSIDE the modal/drawer (otherwise naive-ui's focus
    // trap steals focus and clears the selection before execCommand runs).
    // We also intercept the copy event to force the correct data in, in case
    // the selection is still lost.
    const anchor = (evt?.currentTarget as HTMLElement | undefined) || (document.activeElement as HTMLElement) || document.body
    let ok = false
    try {
      ok = execCopy(text, anchor)
    } catch {
      ok = false
    }
    if (ok) {
      message.success('已复制到剪贴板')
    } else {
      message.error('复制失败，请手动选择复制')
    }
  }

  return { copyText }
}

function execCopy(text: string, anchor: HTMLElement): boolean {
  // mount the textarea inside the same modal/card as the clicked button
  // so the modal's focus trap does not steal focus from it.
  const container = anchor.parentElement || document.body
  const ta = document.createElement('textarea')
  ta.setAttribute('readonly', '')
  ta.value = text
  ta.style.position = 'absolute'
  ta.style.left = '-9999px'
  ta.style.top = '0'
  ta.style.width = '1px'
  ta.style.height = '1px'
  ta.style.opacity = '0'
  container.appendChild(ta)

  let ok = false
  // safety net: force the clipboard payload even if the selection is cleared
  const onCopy = (e: ClipboardEvent) => {
    try {
      e.preventDefault()
      e.clipboardData?.setData('text/plain', text)
      ok = true
    } catch {
      // ignore
    }
  }
  document.addEventListener('copy', onCopy)
  try {
    ta.focus()
    ta.select()
    ta.setSelectionRange(0, text.length)
    document.execCommand('copy')
  } finally {
    document.removeEventListener('copy', onCopy)
    try { container.removeChild(ta) } catch { /* already removed */ }
  }
  return ok
}
