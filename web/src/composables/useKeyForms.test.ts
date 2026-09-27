/**
 * useKeyForms 的行为契约（两套 Keys 页面共用的密钥表单状态）。
 *
 * 守的规则：
 *   - 名称必填（只含空白也算空）、保存前 trim（与服务端 TrimSpace 口径一致）；
 *   - 前端重名即时提示以「trim 后比较 + 排除自己」为准，且只在真正改名时拦；
 *   - allowed_models 原样提交：空数组 = 不限，客户端不做 trim/去重（服务端只做精确匹配）；
 *   - 失败提示要取服务端 error 原文，取不到再退到 message。
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import { emptyKeyCreateForm, keyLabelTaken, useKeyForms } from './useKeyForms'
import { createApiMock, type ApiMock } from '@/test/apiMock'
import { lastMessage, messagesOf, resetMessages } from '@/test/naiveMessage'
import type { ExtKey } from '@/api/keys'

vi.mock('naive-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('naive-ui')>()
  const { messageApi } = await import('@/test/naiveMessage')
  return { ...actual, useMessage: () => messageApi }
})

function key(over: Partial<ExtKey> = {}): ExtKey {
  return {
    id: 1,
    key: 'all-sk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    label: 'app',
    remark: '',
    enabled: true,
    daily_token_limit: 0,
    monthly_token_limit: 0,
    allowed_models: [],
    ...over,
  }
}

let api: ApiMock
let reloads: number
let ensureCalls: number

beforeEach(() => {
  resetMessages()
  reloads = 0
  ensureCalls = 0
  api = createApiMock().install()
})

function mount(keys: ExtKey[] = []) {
  const keysRef = ref(keys)
  const forms = useKeyForms(keysRef, {
    reload: async () => { reloads++ },
    ensureModelOptions: () => { ensureCalls++ },
  })
  return { keysRef, ...forms }
}

describe('useKeyForms 纯函数', () => {
  // 规则：零值表单表示「不限额度、不限模型」——默认值不能是 undefined（后端按 0 处理）
  it('emptyKeyCreateForm 给出零值表单（额度 0 = 不限、白名单空 = 不限）', () => {
    expect(emptyKeyCreateForm()).toEqual({ label: '', remark: '', daily_token_limit: 0, monthly_token_limit: 0, allowed_models: [] })
  })

  // 规则：重名判定只 trim 库里的 label，传入的 label 由调用方先 trim
  // （saveCreate/saveEdit 都是先 label.trim() 再比），且比较区分大小写、空名不参与
  it('keyLabelTaken：trim 库中名称后比较、空名不算重名、可排除自己', () => {
    const keys = [key({ id: 1, label: ' App ' }), key({ id: 2, label: 'other' })]
    expect(keyLabelTaken(keys, 'App')).toBe(true)
    expect(keyLabelTaken(keys, '')).toBe(false)
    expect(keyLabelTaken(keys, 'app2')).toBe(false)
    expect(keyLabelTaken(keys, 'app')).toBe(false) // 区分大小写
    // 调用方必须自己 trim：未 trim 的入参匹配不上（当前实现的非对称点）
    expect(keyLabelTaken(keys, ' App ')).toBe(false)
    // 排除自己：编辑不改名时不能被自己卡住
    expect(keyLabelTaken(keys, 'App', 1)).toBe(false)
    expect(keyLabelTaken(keys, 'App', 2)).toBe(true)
  })
})

describe('useKeyForms 新增', () => {
  // 规则：名称只含空白等于没填，直接拦下不发请求
  it('空 / 纯空白名称：提示「请填写名称」且不创建', async () => {
    const f = mount()
    f.openCreate()
    f.createForm.value.label = '   '
    await f.saveCreate()

    expect(messagesOf('warning')).toEqual(['请填写名称'])
    expect(api.callsTo('post', '/keys')).toHaveLength(0)
  })

  // 规则：前端即时重名提示（最终以服务端为准），命中就不发请求
  it('重名：提示「名称已存在」且不创建', async () => {
    const f = mount([key({ id: 1, label: 'app' })])
    f.openCreate()
    f.createForm.value.label = ' app '
    await f.saveCreate()

    expect(messagesOf('warning')).toEqual(['名称已存在，请换一个'])
    expect(api.callsTo('post', '/keys')).toHaveLength(0)
  })

  // 规则：新建成功后要展示生成的 key、切到「已生成」态并刷新列表；label/remark 提交前 trim
  it('创建成功：提交 trim 后的字段与白名单，切到已生成态并刷新列表', async () => {
    api.on('post', '/keys', { data: key({ id: 5, key: 'all-sk-new', label: 'newapp' }) })
    const f = mount()
    f.openCreate()
    expect(ensureCalls).toBe(1) // 打开表单时懒加载模型选项

    f.createForm.value.label = '  newapp  '
    f.createForm.value.remark = '  备注  '
    f.createForm.value.daily_token_limit = 1000
    f.createForm.value.monthly_token_limit = 20000
    f.createForm.value.allowed_models = ['up/model', 'alias']
    await f.saveCreate()

    const posts = api.callsTo('post', '/keys')
    expect(posts).toHaveLength(1)
    expect(posts[0].data).toEqual({
      label: 'newapp',
      remark: '备注',
      daily_token_limit: 1000,
      monthly_token_limit: 20000,
      allowed_models: ['up/model', 'alias'],
    })
    expect(f.newlyCreatedKey.value).toBe('all-sk-new')
    expect(f.createModalState.value).toBe('done')
    expect(reloads).toBe(1)
  })

  // 规则（当前契约）：allowed_models 原样提交——空数组表示不限；客户端不做 trim/去重，
  // 服务端也只做精确匹配。是否要在这里归一由产品决定，改动必须同步改这条断言。
  it('allowed_models 原样提交：空数组 = 不限，不做客户端 trim/去重', async () => {
    api.on('post', '/keys', { data: key() })
    const f = mount()
    f.openCreate()
    f.createForm.value.label = 'x'
    f.createForm.value.allowed_models = []
    await f.saveCreate()
    expect(api.callsTo('post', '/keys')[0].data.allowed_models).toEqual([])

    const g = mount()
    g.openCreate()
    g.createForm.value.label = 'y'
    g.createForm.value.allowed_models = [' a/b ', 'a/b', 'a/b']
    await g.saveCreate()
    expect(api.callsTo('post', '/keys')[1].data.allowed_models).toEqual([' a/b ', 'a/b', 'a/b'])
  })

  // 规则：创建失败要把服务端 error 原文带出来；服务端没给结构就退到 message
  it('创建失败：优先展示服务端 error，其次退到 Error.message', async () => {
    api.on('post', '/keys', { status: 400, data: { error: 'key label already exists' } })
    const f = mount()
    f.openCreate()
    f.createForm.value.label = 'dup'
    await f.saveCreate()
    expect(lastMessage()).toBe('创建失败：key label already exists')

    api.on('post', '/keys', () => { throw new Error('Network Error') })
    const g = mount()
    g.openCreate()
    g.createForm.value.label = 'dup2'
    await g.saveCreate()
    expect(lastMessage()).toBe('创建失败：Network Error')
  })

  // 规则：openCreate/resetCreateForm 回到初始态（「再新增」按钮走同一条路）
  it('resetCreateForm 清空表单、回到填写态、清掉刚生成的 key', async () => {
    api.on('post', '/keys', { data: key({ key: 'all-sk-first' }) })
    const f = mount()
    f.openCreate()
    f.createForm.value.label = 'a'
    f.createForm.value.allowed_models = ['x']
    await f.saveCreate()
    expect(f.createModalState.value).toBe('done')

    f.resetCreateForm()

    expect(f.createModalState.value).toBe('form')
    expect(f.newlyCreatedKey.value).toBe('')
    expect(f.createForm.value).toEqual(emptyKeyCreateForm())
    expect(ensureCalls).toBe(2)
  })
})

describe('useKeyForms 编辑', () => {
  // 规则：编辑表单要用当前行的值填充（白名单缺省按空数组）
  it('openEdit 用行数据填充表单并打开弹窗', () => {
    const row = key({ id: 3, label: 'app', remark: 'r', enabled: false, daily_token_limit: 7, monthly_token_limit: 8, allowed_models: ['m'] })
    const f = mount([row])
    f.openEdit(row)

    expect(f.editing.value).toEqual(row)
    expect(f.showEditModal.value).toBe(true)
    expect(f.editForm.value).toEqual({ label: 'app', remark: 'r', enabled: false, daily_token_limit: 7, monthly_token_limit: 8, allowed_models: ['m'] })
    expect(ensureCalls).toBe(1)
  })

  // 规则：老数据（后端 allowed_models 为 null）进编辑表单要能正常渲染，不能是 null
  it('openEdit 遇到 allowed_models 为 null 的老数据按空数组处理', () => {
    const row = key({ allowed_models: null })
    const f = mount([row])
    f.openEdit(row)
    expect(f.editForm.value.allowed_models).toEqual([])
  })

  // 规则：不改名的保存不能被「别人的重名」卡住（唯一约束是后加的，老数据可能本就重名）
  it('未改名时即使列表里有同名也不拦，照常提交', async () => {
    const row = key({ id: 1, label: 'app' })
    const dup = key({ id: 2, label: 'app' })
    api.on('put', '/keys/1', { data: row })
    const f = mount([row, dup])
    f.openEdit(row)
    await f.saveEdit()

    expect(api.callsTo('put', '/keys/1')).toHaveLength(1)
    expect(api.callsTo('put', '/keys/1')[0].data.label).toBe('app')
    expect(lastMessage()).toBe('已保存')
    expect(reloads).toBe(1)
    expect(f.showEditModal.value).toBe(false)
    expect(f.editing.value).toBeNull()
  })

  // 规则：真正改名时前端先拦重名（服务端仍会兜底 400）
  it('改名撞上已存在的名称时提示并拦下', async () => {
    const row = key({ id: 1, label: 'app' })
    const other = key({ id: 2, label: 'taken' })
    const f = mount([row, other])
    f.openEdit(row)
    f.editForm.value.label = ' taken '
    await f.saveEdit()

    expect(messagesOf('warning')).toEqual(['名称已存在，请换一个'])
    expect(api.callsTo('put', '/keys/1')).toHaveLength(0)
  })

  // 规则：编辑保存提交 trim 后的 label/remark、启用状态、限额与白名单
  it('编辑保存：提交 trim 后的字段与启用状态', async () => {
    const row = key({ id: 4, label: 'app', remark: 'old' })
    api.on('put', '/keys/4', { data: row })
    const f = mount([row])
    f.openEdit(row)
    f.editForm.value.label = '  renamed  '
    f.editForm.value.remark = ' 新备注 '
    f.editForm.value.enabled = false
    f.editForm.value.daily_token_limit = 5
    f.editForm.value.monthly_token_limit = 6
    f.editForm.value.allowed_models = ['a/b']
    await f.saveEdit()

    expect(api.callsTo('put', '/keys/4')[0].data).toEqual({
      label: 'renamed',
      remark: '新备注',
      enabled: false,
      daily_token_limit: 5,
      monthly_token_limit: 6,
      allowed_models: ['a/b'],
    })
  })

  // 规则：名称必填与重名校验在编辑态同样生效
  it('编辑时名称清空：提示且不提交', async () => {
    const row = key({ id: 1, label: 'app' })
    const f = mount([row])
    f.openEdit(row)
    f.editForm.value.label = '   '
    await f.saveEdit()

    expect(messagesOf('warning')).toEqual(['请填写名称'])
    expect(api.callsTo('put', '/keys/1')).toHaveLength(0)
  })

  // 规则：保存失败不能关弹窗（否则用户以为改成功了），错误文案取服务端原文
  it('编辑保存失败：弹窗保持打开并展示服务端错误', async () => {
    const row = key({ id: 1, label: 'app' })
    api.on('put', '/keys/1', { status: 409, data: { error: 'key label already exists' } })
    const f = mount([row])
    f.openEdit(row)
    f.editForm.value.label = 'renamed'
    await f.saveEdit()

    expect(lastMessage()).toBe('保存失败：key label already exists')
    expect(f.showEditModal.value).toBe(true)
    expect(f.editing.value).toEqual(row)
    expect(reloads).toBe(0)
  })

  // 规则：没选中行时保存是空操作（页面在弹窗被关掉后仍可能触发一次保存）
  it('没有选中行时 saveEdit 直接返回', async () => {
    const f = mount()
    await f.saveEdit()
    expect(api.calls).toHaveLength(0)
    expect(messagesOf('error')).toEqual([])
  })
})
