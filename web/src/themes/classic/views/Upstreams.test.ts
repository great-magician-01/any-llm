/**
 * 经典皮肤「上游管理」页的组件测试（本仓库第一个真实页面测试）。
 *
 * 断言只落在两件事上：
 *   1. 用户看到什么（渲染出来的行、警告/错误文案、确认框）；
 *   2. 页面发出了什么请求（method + url + params/body——走真实 axios，假后端只换 adapter）。
 *
 * 守的业务规则见每个 it 上方的注释。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Upstreams from './Upstreams.vue'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { attachFile, bodyHas, bodyText, clickButton, fillInput, flush, inputByLabel, mountPage } from '@/test/ui'
import { lastMessage, messageCalls, messagesOf, resetMessages } from '@/test/naiveMessage'
import type { Upstream } from '@/api/upstreams'

// useMessage 换成本地记录器：断言「页面弹给用户的那句话」，不必去 DOM 里捞 message 浮层。
// 其余 naive-ui 导出保持原样，组件照常真实渲染。
vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

function upstream(over: Partial<Upstream> = {}): Upstream {
  return {
    id: 1,
    name: 'deepseek',
    base_url: 'https://api.deepseek.com',
    api_key: 'sk-***masked***',
    format: 'openai',
    remark: '',
    enabled: true,
    daily_token_limit: 0,
    monthly_token_limit: 0,
    max_concurrent: 100,
    expires_at: null,
    model_count: 3,
    ...over,
  }
}

/** 本页共用的基础路由：列表 + 余额快照 + 开页自动刷新余额 */
function baseRoutes(api: ApiMock, list: Upstream[] = [upstream()]) {
  api.on('get', '/upstreams', { data: { data: list } })
  api.on('get', '/balances', { data: { data: [] } })
  api.on('post', '/balances', { data: { data: [] } })
}

let api: ApiMock
let wrapper: ReturnType<typeof mountPage> | null = null

beforeEach(() => {
  resetMessages()
  api = createApiMock().install()
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  api.restore()
})

/**
 * 打开页面并等首屏请求落地。
 * setup 在挂载前执行——追加的路由必须排在基础路由之后、首屏请求之前登记，
 * 否则 mock 的顺序消费会被首屏那次请求抢走。
 */
async function open(list: Upstream[] = [upstream()], setup?: (mock: ApiMock) => void) {
  baseRoutes(api, list)
  setup?.(api)
  wrapper = mountPage(Upstreams)
  await flush()
  return wrapper
}

function fileInput(): HTMLInputElement {
  const input = document.querySelector('input[type="file"]')
  if (!input) throw new Error('页面上没有文件选择框')
  return input as HTMLInputElement
}

async function pickConfigFile(content: string): Promise<void> {
  const input = fileInput()
  attachFile(input, content)
  input.dispatchEvent(new Event('change'))
  await flush()
}

describe('Upstreams 页（经典皮肤）', () => {
  // 规则：列表默认只查「启用中」，开页即加载余额快照并静默刷新一轮厂商余额
  it('打开页面按「启用中」档位查询，并把上游渲染进表格', async () => {
    await open()

    // 列表口径：只查启用中的（Dashboard/Keys 才用全量）
    expect(api.callsTo('get', '/upstreams')).toHaveLength(1)
    expect(api.calls[0].params).toEqual({ status: 'enabled' })
    // 余额快照与「开页静默刷新」都发生在首屏
    expect(api.callsTo('get', '/balances')).toHaveLength(1)
    expect(api.callsTo('post', '/balances')).toHaveLength(1)
    // 用户看到行
    expect(bodyText()).toContain('deepseek')
    expect(bodyText()).toContain('https://api.deepseek.com')
  })

  // 规则：导入前必须先做结构校验，校验不过要在页面上给出可见错误，且绝不发导入请求
  it('导入非法 JSON 文件：给出可见错误提示且不发送导入请求', async () => {
    await open()
    await pickConfigFile('{ not json')

    expect(messagesOf('error')).toContain('导入失败：不是有效的 JSON 文件')
    expect(api.callsTo('post', '/config/import')).toHaveLength(0)
    // 连确认弹窗都不该出现
    expect(bodyHas('开始导入')).toBe(false)
  })

  // 规则：JSON 合法但缺 upstreams/aliases 字段同样要拦在前端，错误文案要指出缺什么
  it('导入结构不符的配置：错误提示说明缺字段，且不发送导入请求', async () => {
    await open()
    await pickConfigFile('{"version":1,"upstreams":[]}')

    expect(messagesOf('error')).toContain('导入失败：配置文件缺少 upstreams / aliases 字段')
    expect(api.callsTo('post', '/config/import')).toHaveLength(0)
  })

  // 规则：合法文件先弹确认摘要（让用户知道会覆盖什么），确认后才发导入请求并刷新列表
  it('导入合法配置：先显示摘要，确认后发送导入请求、提示结果并刷新列表', async () => {
    await open()
    const file = {
      version: 1,
      upstreams: [{ name: 'moonshot', base_url: 'https://api.kimi.com', api_key: 'sk-x', format: 'openai', enabled: false }],
      aliases: [],
    }
    await pickConfigFile(JSON.stringify(file))

    // 摘要弹窗（describeConfigFile 的文案）
    expect(bodyHas('该文件包含 1 个上游、0 个别名')).toBe(true)
    expect(api.callsTo('post', '/config/import')).toHaveLength(0)
    const before = api.callsTo('get', '/upstreams').length

    api.on('post', '/config/import', {
      data: { upstreams_created: 1, upstreams_updated: 0, aliases_created: 0, aliases_updated: 0, aliases_skipped: 0, bindings_dropped: 0 },
    })
    await clickButton('开始导入')

    const posts = api.callsTo('post', '/config/import')
    expect(posts).toHaveLength(1)
    expect(posts[0].data).toEqual(file)
    expect(messagesOf('success').join('|')).toContain('导入完成：上游新建 1')
    expect(messagesOf('success').join('|')).toContain('丢弃绑定 0')
    // 文件里有禁用上游而当前只看「启用中」：必须提醒否则用户以为没导进去
    expect(messagesOf('warning').join('|')).toContain('当前只看「启用中」')
    // 导入后列表刷新
    expect(api.callsTo('get', '/upstreams').length).toBe(before + 1)
  })

  // 规则：reachable=true 但 ok=false 是「连上了、上游给了非 2xx」（认证失败），
  // 必须与 reachable=false（压根连不上）显示成不同文案，否则用户无法判断该改 key 还是改地址
  it('行内连通性测试：认证失败与网络不可达渲染成两种不同文案', async () => {
    await open()
    api.on('post', '/upstreams/1/test', { data: { ok: false, reachable: true, status: 401, latency_ms: 12, detail: 'Unauthorized' } })
    api.on('post', '/upstreams/1/test', { data: { ok: false, reachable: false, latency_ms: 5, detail: 'dial tcp: i/o timeout' } })

    await clickButton('测试')
    const authFailed = lastMessage()
    expect(authFailed).toContain('已连通但认证失败（HTTP 401）')
    expect(authFailed).toContain('deepseek')

    await clickButton('测试')
    const unreachable = lastMessage()

    // 两种结果必须是不同的文案，且各自带自己的判据
    expect(unreachable).not.toBe(authFailed)
    expect(unreachable).toContain('无法连通：dial tcp: i/o timeout')
    expect(unreachable).not.toContain('认证失败')
    expect(authFailed).not.toContain('无法连通')
    // 两次都发到 by-id 端点
    expect(api.callsTo('post', '/upstreams/1/test')).toHaveLength(2)
  })

  // 规则：新增态的表单内测试打的是「未保存配置」端点（/upstreams/test），
  // 且三档结果就地渲染成不同类型的 alert——警告（端点不规范）与错误（不可达）必须是两种
  it('表单内连通性测试：新增态测未保存配置，警告/错误两档文案不同', async () => {
    await open()
    await clickButton('添加上游')
    expect(bodyHas('快捷配置')).toBe(true)
    await fillInput(inputByLabel('Base URL'), 'https://api.example.com/v1')

    api.on('post', '/upstreams/test', { data: { ok: false, reachable: true, status: 404, latency_ms: 9, detail: '' } })
    api.on('post', '/upstreams/test', { data: { ok: false, reachable: false, latency_ms: 3, detail: 'ENOTFOUND' } })

    await clickButton('测试连通性')
    const posts = api.callsTo('post', '/upstreams/test')
    expect(posts).toHaveLength(1)
    expect(posts[0].data.base_url).toBe('https://api.example.com/v1')

    const first = document.querySelector('.n-alert')?.textContent ?? ''
    expect(first).toContain('可连通，但 /models 返回 HTTP 404')

    await clickButton('测试连通性')
    const second = document.querySelector('.n-alert')?.textContent ?? ''
    expect(second).toContain('无法连通：ENOTFOUND')
    expect(second).not.toBe(first)
    expect(second).not.toContain('404')
    expect(api.callsTo('post', '/upstreams/test')).toHaveLength(2)
  })

  // 规则：Base URL 是测试的必填项，缺了只提示、不发请求（避免拿空地址去打上游）
  it('表单内连通性测试缺 Base URL 时只提示、不发请求', async () => {
    await open()
    await clickButton('添加上游')

    await clickButton('测试连通性')

    expect(messagesOf('warning')).toContain('请先填写 Base URL')
    expect(api.callsTo('post', '/upstreams/test')).toHaveLength(0)
  })

  // 规则：禁用走 PUT {enabled:false}，随后整列重查（该行是否还属于当前档位由后端说了算）；
  // 从「启用中」档位禁用成功后，该行必须从列表消失
  it('禁用上游：PUT enabled=false，并按当前档位重查列表（行随之消失）', async () => {
    await open([upstream()], (mock) => {
      mock.on('put', '/upstreams/1', { data: upstream({ enabled: false }) })
      mock.on('get', '/upstreams', { data: { data: [] } })
    })

    const sw = document.querySelector('.n-switch') as HTMLElement
    expect(sw).toBeTruthy()
    sw.click()
    await flush()

    const puts = api.callsTo('put', '/upstreams/1')
    expect(puts).toHaveLength(1)
    expect(puts[0].data).toEqual({ enabled: false })
    // 重查仍带当前档位
    const lists = api.callsTo('get', '/upstreams')
    expect(lists).toHaveLength(2)
    expect(lists[1].params).toEqual({ status: 'enabled' })
    // 用户看到该行离开「启用中」列表
    expect(bodyText()).not.toContain('deepseek')
  })

  // 规则：禁用失败不能乐观改开关（否则界面说禁用、后端还在跑），要把服务端错误原文告诉用户
  it('禁用失败：保持开关原状态并展示服务端错误原文', async () => {
    await open()
    api.on('put', '/upstreams/1', { status: 500, data: { error: '禁用失败：数据库不可用' } })

    const sw = document.querySelector('.n-switch') as HTMLElement
    sw.click()
    await flush()

    expect(messageCalls.some((c) => c.level === 'error' && c.text.includes('禁用失败：数据库不可用'))).toBe(true)
    // 开关仍是「开」
    expect(document.querySelector('.n-switch')?.getAttribute('aria-checked')).toBe('true')
  })

  // 规则：删除必须经过二次确认；确认后 DELETE 该行并刷新列表
  it('删除上游：确认框出现后确认，DELETE 该 id 并刷新列表', async () => {
    await open()
    api.on('delete', '/upstreams/1', { data: {} })
    const listsBefore = api.callsTo('get', '/upstreams').length

    await clickButton('删除')
    // 二次确认文案就位，此刻还没有发出删除
    expect(bodyText()).toContain('确定删除？')
    expect(api.callsTo('delete', '/upstreams/1')).toHaveLength(0)

    await clickButton('确认')

    expect(api.callsTo('delete', '/upstreams/1')).toHaveLength(1)
    expect(api.callsTo('get', '/upstreams').length).toBe(listsBefore + 1)
  })

  // 规则：取消二次确认不得发出任何删除请求
  it('删除上游：取消确认时不发请求', async () => {
    await open()

    await clickButton('删除')
    await clickButton('取消')

    expect(api.callsTo('delete', '/upstreams/1')).toHaveLength(0)
  })

  // 规则：过期/永久两种有效期状态在列表里必须是可区分的展示
  it('有效期列：永久有效与已过期渲染成不同文案', async () => {
    await open([
      upstream({ id: 1, name: 'forever', expires_at: null }),
      upstream({ id: 2, name: 'stale', expires_at: '2020-01-01T00:00:00+08:00' }),
    ])

    const text = bodyText()
    expect(text).toContain('永久')
    expect(text).toContain('已过期')
  })

  it('刷新按钮会重新拉取列表与余额快照', async () => {
    await open()
    const listBefore = api.callsTo('get', '/upstreams').length
    const balBefore = api.callsTo('get', '/balances').length

    // 头部刷新按钮是纯图标按钮（无文案），只按位置（页头右侧）取
    const refresh = document.querySelector('.page-header-side button') as HTMLButtonElement
    expect(refresh).toBeTruthy()
    refresh.click()
    await flush()

    expect(api.callsTo('get', '/upstreams').length).toBe(listBefore + 1)
    expect(api.callsTo('get', '/balances').length).toBe(balBefore + 1)
  })

  // 规则：附加端点在表单里增删成行，创建时随表单一起提交
  it('附加格式端点：添加一行并随创建请求提交', async () => {
    await open()
    await clickButton('添加上游')
    await fillInput(inputByLabel('名称'), 'ds')
    await fillInput(inputByLabel('Base URL'), 'https://api.deepseek.com/v1')
    await fillInput(inputByLabel('API Key'), 'sk-real')

    await clickButton('添加端点')
    const epInput = document.querySelector('input[placeholder="该格式端点的 Base URL"]') as HTMLInputElement
    expect(epInput).toBeTruthy()
    await fillInput(epInput, 'https://api.deepseek.com/anthropic')

    api.on('post', '/upstreams', { data: upstream({ id: 2, name: 'ds' }) })
    await clickButton('添加')

    const posts = api.callsTo('post', '/upstreams')
    expect(posts).toHaveLength(1)
    // 附加端点默认取第一个未被主格式占用的协议（主格式 openai → anthropic）
    expect(posts[0].data.extra_endpoints).toEqual([{ format: 'anthropic', base_url: 'https://api.deepseek.com/anthropic' }])
    expect(messagesOf('success')).toContain('已添加')
  })

  // 规则：主格式与附加端点撞车时在表单里给出可操作的提示，并阻止保存
  // （后端同样 400，但用户应该更早看到该删哪一行）
  it('主格式与附加端点重复：提示删除重复项且不发保存请求', async () => {
    await open()
    await clickButton('添加上游')
    await fillInput(inputByLabel('名称'), 'ds')
    await fillInput(inputByLabel('Base URL'), 'https://api.deepseek.com/v1')
    await clickButton('添加端点')
    const epInput = document.querySelector('input[placeholder="该格式端点的 Base URL"]') as HTMLInputElement
    await fillInput(epInput, 'https://api.deepseek.com/anthropic')
    // 此刻尚无重复（主格式 openai + 附加 anthropic）
    expect(bodyHas('请删除重复项')).toBe(false)

    // 把主格式切到 Anthropic → 与附加端点撞车，提示出现
    const radio = Array.from(document.querySelectorAll('.n-radio'))
      .find(el => (el.textContent ?? '').replace(/\s+/g, '') === 'Anthropic') as HTMLElement
    expect(radio).toBeTruthy()
    radio.click()
    await flush()
    expect(bodyHas('附加端点与主格式重复')).toBe(true)

    // 保存被拦截：提示 + 不发创建请求
    await clickButton('添加')
    expect(messagesOf('error').join('|')).toContain('请删除重复项')
    expect(api.callsTo('post', '/upstreams')).toHaveLength(0)

    // 移除重复行后提示消失、可以保存
    await clickButton('移除')
    expect(bodyHas('请删除重复项')).toBe(false)
    api.on('post', '/upstreams', { data: upstream({ id: 2, name: 'ds', format: 'anthropic' }) })
    await clickButton('添加')
    const posts = api.callsTo('post', '/upstreams')
    expect(posts).toHaveLength(1)
    expect(posts[0].data.format).toBe('anthropic')
    expect(posts[0].data.extra_endpoints).toEqual([])
  })

  // 规则：编辑带附加端点的上游时端点回填进表单；直接保存原样带回（不能丢）
  it('编辑时回填附加端点，保存原样提交', async () => {
    const withExtras = upstream({
      extra_endpoints: [{ format: 'anthropic', base_url: 'https://api.deepseek.com/anthropic' }],
    })
    await open([withExtras])
    api.on('put', '/upstreams/1', { data: withExtras })

    await clickButton('编辑')
    const epInput = document.querySelector('input[placeholder="该格式端点的 Base URL"]') as HTMLInputElement
    expect(epInput?.value).toBe('https://api.deepseek.com/anthropic')

    await clickButton('保存')
    const puts = api.callsTo('put', '/upstreams/1')
    expect(puts).toHaveLength(1)
    expect(puts[0].data.extra_endpoints).toEqual([{ format: 'anthropic', base_url: 'https://api.deepseek.com/anthropic' }])
    expect(messagesOf('success')).toContain('已保存')
  })
})
