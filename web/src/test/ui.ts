/**
 * 组件测试的公共 DOM 工具：按「用户看到的文字」定位按钮、填表、选文件。
 *
 * 两个约定：
 *   - 挂载时套上 App.vue 同款的 NConfigProvider(zhCN)：naive-ui 默认 locale 是
 *     enUS，确认框按钮会是 Confirm/Cancel，和真实界面（确认/取消）不一致。
 *   - 断言直接打在 document.body 上（组件 attachTo body）：naive-ui 的浮层
 *     （popconfirm、message）teleport 到 body，只看 wrapper 会漏掉它们。
 *
 * 定位按钮只用可见文案，不用 naive-ui 的内部类名——类名是实现细节，升级会变。
 */
import { mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, nextTick, type Component } from 'vue'
import naive, { NConfigProvider, dateZhCN, zhCN } from 'naive-ui'

export type AnyWrapper = VueWrapper<any>

/** 挂载整页组件：装 naive-ui，让模板里的 n-card / n-data-table 等全局组件可用 */
export function mountPage(component: Component): AnyWrapper {
  const Host = defineComponent({
    render: () =>
      h(NConfigProvider, { locale: zhCN, dateLocale: dateZhCN }, { default: () => h(component) }),
  })
  return mount(Host, { global: { plugins: [naive] }, attachTo: document.body })
}

/** 文案归一：忽略标签之间的换行/缩进空白（模板里的多行标签很常见） */
function norm(value: string): string {
  return value.replace(/\s+/g, '')
}

function text(el: Element): string {
  return norm(el.textContent ?? '')
}

export function buttonsIn(root: ParentNode = document.body): HTMLButtonElement[] {
  return Array.from(root.querySelectorAll('button'))
}

/** 当前可见的按钮文案清单，断言失败时用来定位问题 */
export function buttonTexts(): string[] {
  return buttonsIn().map(text).filter(Boolean)
}

export async function clickButton(label: string): Promise<void> {
  const btn = buttonsIn().find((b) => text(b) === norm(label))
  if (!btn) {
    throw new Error(`未找到按钮「${label}」，当前按钮：${buttonTexts().join(' | ')}`)
  }
  btn.click()
  await flush()
}

export function bodyText(): string {
  return norm(document.body.textContent ?? '')
}

/** body 里是否出现某段文案（同样忽略空白，断言可以写成用户看到的原句） */
export function bodyHas(needle: string): boolean {
  return bodyText().includes(norm(needle))
}

/** 按 label 文案取表单项里的输入框（n-form-item 的 label 与控件同在一个 item 内） */
export function inputByLabel(label: string): HTMLInputElement {
  const items = Array.from(document.body.querySelectorAll('.n-form-item'))
  const item = items.find((i) => text(i.querySelector('.n-form-item-label') ?? i) === norm(label))
  const input = item?.querySelector('input')
  if (!input) {
    const labels = items.map((i) => text(i.querySelector('.n-form-item-label') ?? i))
    throw new Error(`未找到表单项「${label}」，当前表单项：${labels.join(' | ')}`)
  }
  return input as HTMLInputElement
}

export async function fillInput(input: HTMLInputElement, value: string): Promise<void> {
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await flush()
}

/** 给 <input type="file"> 塞一个文件（happy-dom 的 files 是只读的，只能改写属性） */
export function attachFile(input: HTMLInputElement, content: string, name = 'config.json'): File {
  const file = new File([content], name, { type: 'application/json' })
  // 不依赖 happy-dom 对 Blob.text() 的实现：固定返回给定内容
  Object.defineProperty(file, 'text', { value: async () => content, configurable: true })
  Object.defineProperty(input, 'files', { value: [file], configurable: true })
  return file
}

/** 等所有微任务与 DOM 更新落地（页面上的异步加载/刷新都走一遍） */
export async function flush(times = 3): Promise<void> {
  for (let i = 0; i < times; i++) {
    await new Promise((resolve) => setTimeout(resolve, 0))
    await nextTick()
  }
}
