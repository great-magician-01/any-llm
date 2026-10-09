/**
 * 双主题 parity：经典（/upstreams）与毛玻璃（/glass/upstreams）是两套逐行重复的
 * 实现，同一批业务规则必须落在同一个共享实现上。人眼 review 抓不住「只改了一边」，
 * 这里用两层断言把它变成红灯：
 *
 *   1. 源码层：两边都从同一个 composable / utils 引入同一批业务函数，且不在页面里
 *      私藏一份同名实现（复制一份就必然会被改歪）；
 *   2. 运行时层：同样的假后端 + 同样的用户操作（开页 / 行内测试 / 删除确认），
 *      两边发出的请求序列与给用户的提示必须完全一致。
 *
 * 允许两套页面在「样式」上不同，但列宽规格与自适应逻辑共用同一份
 * （useTableFit：列宽随容器宽度连续压缩、scroll-x 永不超出容器），所以这里
 * 只比文案、请求与业务分支，不比 class/style。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ClassicUpstreams from './classic/views/Upstreams.vue'
import GlassUpstreams from './glass/views/GlassUpstreams.vue'
// ?raw 直接取 SFC 源码做源码级 parity 断言（vite/client 已声明 *?raw 模块）
import CLASSIC_SRC from './classic/views/Upstreams.vue?raw'
import GLASS_SRC from './glass/views/GlassUpstreams.vue?raw'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { bodyHas, bodyText, clickButton, flush, mountPage } from '@/test/ui'
import { messageCalls, resetMessages } from '@/test/naiveMessage'

vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

/** 两边都必须从共享实现引入的业务函数（页面私有实现一律算回归） */
const SHARED_IMPORTS = [
  '@/composables/useUpstreamList',
  '@/composables/useUpstreamColumns',
  '@/composables/useTableFit',
  './composables/useUpstreamList',
  '../composables/useUpstreamList',
  '@/utils/connectivity',
  '@/utils/configTransfer',
  '@/utils/upstreamStatus',
  '@/utils/upstreamTag',
  '@/utils/upstreamPresets',
  '@/utils/format',
  '@/utils/balance',
  '@/api/upstreams',
  '@/api/balances',
  '@/api/config',
]

const UPSTREAM: any = {
  id: 1,
  name: 'deepseek',
  base_url: 'https://api.deepseek.com',
  api_key: 'sk-***masked***',
  format: 'openai',
  remark: '',
  tag: 'official',
  enabled: true,
  daily_token_limit: 0,
  monthly_token_limit: 0,
  max_concurrent: 100,
  expires_at: null,
  model_count: 3,
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
 * 用同一套假后端跑一遍「用户会做的三件事」，返回可比较的可观测结果：
 * 请求序列（method + url + 关键参数/体）、页面文案、给用户的提示。
 */
async function exercise(component: any) {
  // 每次从干净的 DOM 起步：上一次挂载的页面（以及它 teleport 到 body 的确认框）
  // 必须彻底消失，否则 querySelector 会选中上一次的那个页面
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
  resetMessages()
  api.restore()
  api = createApiMock().install()
  // 每次响应都给一份新对象：页面的开关会就地改 row.enabled（乐观更新），
  // 共用同一个引用会让第二套视图拿到被上一轮改过的行
  api.on('get', '/upstreams', () => ({ data: { data: [{ ...UPSTREAM }] } }))
  api.on('get', '/balances', { data: { data: [] } })
  api.on('post', '/balances', { data: { data: [] } })
  api.on('post', '/upstreams/1/test', { data: { ok: false, reachable: true, status: 401, latency_ms: 12, detail: 'Unauthorized' } })
  api.on('put', '/upstreams/1', { data: { ...UPSTREAM, enabled: false } })
  api.on('delete', '/upstreams/1', { data: {} })

  wrapper = mountPage(component)
  await flush()
  const openedText = bodyText()

  // 1) 行内连通性测试：认证失败 → 必须给认证失败文案
  await clickButton('测试')
  const testMessages = [...messageCalls]

  // 2) 禁用：PUT enabled=false + 按当前档位重查
  const sw = document.querySelector('.n-switch') as HTMLElement
  sw.click()
  await flush()

  // 3) 删除：确认后 DELETE + 刷新列表
  resetMessages()
  await clickButton('删除')
  const confirmVisible = bodyHas('确定删除？')
  await clickButton('确认')
  const deleteMessages = [...messageCalls]

  const requests = api.calls.map((c) => {
    const params = Object.entries(c.params)
      .map(([k, v]) => `${k}=${v}`)
      .join(',')
    const body = c.data === undefined || c.data === '' ? '' : ` body=${JSON.stringify(c.data)}`
    return `${c.method.toUpperCase()} ${c.url}${params ? ` ?${params}` : ''}${body}`
  })

  return { openedText, testMessages, deleteMessages, confirmVisible, requests }
}

describe('双主题 parity：上游管理页', () => {
  it('两套视图共用同一批 composable / utils，且不在页面内私藏业务实现', () => {
    for (const [name, src] of [['classic', CLASSIC_SRC], ['glass', GLASS_SRC]] as const) {
      expect(src, `${name} 必须调用共享的 useUpstreamList`).toContain('useUpstreamList()')
      expect(src, `${name} 必须调用共享的 useUpstreamColumns`).toContain('useUpstreamColumns(')
      // 关键共享纯函数两边都要引，任何一个页面自己写一份都会在这里露馅。
      // 表格列（含展开行模型管理面板、余额列）已整体下沉到 useUpstreamColumns，
      // 其中的 expiryLabel/balanceView/formatInt/formatTime 由该共享文件使用，
      // 页面侧不再直接调用，故不在此清单里。
      for (const fn of [
        'connectivityView(', 'parseConfigFile(', 'describeConfigFile(', 'describeImportResult(',
        'downloadJSON(', 'expiryToISO(', 'isoToExpiry(', 'presetSelectOptions(',
        'findPreset(', 'fitColumns(', 'fitScrollX(',
        'useContainerWidth(', 'UPSTREAM_FIT_COLUMNS',
      ]) {
        expect(src, `${name} 必须使用共享实现 ${fn}`).toContain(fn)
      }
    }
    // 两边的共享依赖清单一致（少一个 import = 有一边在用自己的私有实现）
    for (const mod of SHARED_IMPORTS) {
      const inClassic = CLASSIC_SRC.includes(`from '${mod}'`)
      const inGlass = GLASS_SRC.includes(`from '${mod}'`)
      expect(inGlass, `${mod} 在 glass 侧缺失`).toBe(inClassic)
    }
  })

  // 规则：两套页面必须从同一个 composable 取状态（复制一份实现 = 迟早改歪一边）
  it('两套视图都从 useUpstreamList 取同一批业务状态', () => {
    const binding = '{ upstreams, statusFilter, balancesByUpstream, load, setStatusFilter, toggleEnabled }'
    expect(CLASSIC_SRC).toContain(binding)
    expect(GLASS_SRC).toContain(binding)
  })

  it('同样的操作在两套视图里发出同样的请求、给出同样的提示', async () => {
    const classic = await exercise(ClassicUpstreams)
    const classicRequests = classic.requests

    const glass = await exercise(GlassUpstreams)

    expect(glass.requests).toEqual(classicRequests)
    expect(glass.testMessages).toEqual(classic.testMessages)
    expect(glass.deleteMessages).toEqual(classic.deleteMessages)
    expect(glass.confirmVisible).toBe(classic.confirmVisible)
    // 关键业务文案一致（上游名、认证失败判据、确认框）
    expect(classic.openedText).toContain('deepseek')
    expect(glass.openedText).toContain('deepseek')
    expect(classic.testMessages[0].text).toContain('已连通但认证失败（HTTP 401）')
    expect(glass.testMessages[0].text).toBe(classic.testMessages[0].text)
    // 请求序列里必须真的有这几件事，否则「两边一致」可能只是两边都没做
    expect(classicRequests.join('|')).toContain('POST /upstreams/1/test')
    expect(classicRequests.join('|')).toContain('PUT /upstreams/1 body={"enabled":false}')
    expect(classicRequests.join('|')).toContain('DELETE /upstreams/1')
    expect(classicRequests.filter((r) => r.startsWith('GET /upstreams')).length).toBeGreaterThanOrEqual(3)
  })

  // 规则：两套页面只允许样式差异；正文文案不一致说明有一边漏改了
  it('两套视图的正文文案完全一致（只允许样式差异）', async () => {
    const classic = await exercise(ClassicUpstreams)
    const glass = await exercise(GlassUpstreams)

    expect(glass.openedText).toBe(classic.openedText)
  })
})
