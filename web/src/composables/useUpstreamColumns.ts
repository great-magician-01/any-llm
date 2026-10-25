/**
 * 上游管理页的表格列定义（经典/毛玻璃两套 Upstreams 视图共用）。
 *
 * 列定义是约 200 行的渲染函数（含展开行的模型管理面板与「余额/额度」列），
 * 此前在两套页面里逐字重复各一份——抽取动机是双份维护：改一列要在两个文件
 * 同步，漏改一次两套皮肤就悄悄分叉。页面级状态与动作通过 ctx 显式传入，
 * 与 useUpstreamList / useKeyForms 的既有模式一致。
 */
import { computed, h, type ComputedRef, type Ref } from 'vue'
import { NButton, NSpace, NTag, NPopconfirm, NInput, NInputNumber, NSwitch, NText } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { DEFAULT_MODEL_CONTEXT_LENGTH, DEFAULT_MODEL_MAX_OUTPUT_LENGTH, type Upstream, type UpstreamModel } from '@/api/upstreams'
import type { BalanceSnapshot } from '@/api/balances'
import { balanceView, balanceSummary, balanceTooltip, formatFetchedAt } from '@/utils/balance'
import { expiryLabel } from '@/utils/upstreamStatus'
import { tagLabel, tagTone } from '@/utils/upstreamTag'
import { formatInt, formatTime } from '@/utils/format'

/** 展开行里「新模型」的待定参数（每个上游一份）。 */
export interface ModelOpts {
  context_length: number
  max_output_length: number
  multimodal: boolean
}

/** 上游表格依赖的页面状态与动作；两套皮肤各自把页面实现传进来。 */
export interface UpstreamColumnsCtx {
  /** 拟合后的列宽（useTableFit.fitColumns 的产物）。 */
  colW: ComputedRef<Record<string, number>>
  modelsByUpstream: Ref<Record<number, UpstreamModel[]>>
  newModelByUpstream: Ref<Record<number, string>>
  modelOptsFor: (id: number) => ModelOpts
  balancesByUpstream: Ref<Record<number, BalanceSnapshot>>
  fetchingId: Ref<number | null>
  refreshingId: Ref<number | null>
  testingId: Ref<number | null>
  addM: (id: number) => void
  fetchM: (id: number) => void
  delM: (id: number, mid: number) => void
  openModelEdit: (id: number, m: UpstreamModel) => void
  toggleEnabled: (u: Upstream, v: boolean) => void
  edit: (u: Upstream) => void
  del: (id: number) => void
  testRow: (u: Upstream) => void
  refreshB: (id: number) => void
  openHistory: (u: Upstream) => void
}

/** 上下文长度常用档位（展开行与模型编辑弹窗共用）。 */
export const CTX_PRESETS = [
  { label: '200k', value: 200000 },
  { label: '400k', value: 400000 },
  { label: '1M', value: 1000000 },
]

function fmtK(n: number): string {
  return n >= 1000 ? `${Math.round(n / 1000)}k` : String(n)
}

export function useUpstreamColumns(ctx: UpstreamColumnsCtx) {
  const historyColumns: DataTableColumns<BalanceSnapshot> = [
    { title: '时间', key: 'created_at', width: 170, render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, formatTime(row.created_at)) },
    { title: '厂商', key: 'vendor', width: 110, render: (row) => h(NTag, { size: 'small', bordered: false }, { default: () => row.vendor }) },
    { title: '内容', key: 'payload', render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, balanceSummary(row) ?? '-') },
  ]

  // columns 是 computed：容器宽度变化（colW）只重建列配置，不逐像素重算
  const columns = computed<DataTableColumns<Upstream>>(() => [
    {
      type: 'expand', width: ctx.colW.value.expand, expandable: () => true, renderExpand: (row) => {
        const id = row.id as number
        const models = ctx.modelsByUpstream.value[id] || []
        const opts = ctx.modelOptsFor(id)
        return h('div', { style: 'padding: 8px 0 16px 24px' }, [
          h('div', { class: 'toolbar', style: 'margin-bottom: 8px; display: flex; align-items: center; gap: 8px; flex-wrap: wrap' }, [
            h(NInput, {
              value: ctx.newModelByUpstream.value[id] || '',
              'onUpdate:value': (v: string) => { ctx.newModelByUpstream.value[id] = v },
              placeholder: '模型名，如 gpt-4o',
              style: 'width: 240px',
              onKeyup: (e: KeyboardEvent) => { if (e.key === 'Enter') ctx.addM(id) },
            }),
            h('div', { style: 'display: flex; align-items: center; gap: 4px' }, [
              h(NInputNumber, {
                value: opts.context_length,
                'onUpdate:value': (v: number | null) => { opts.context_length = v ?? DEFAULT_MODEL_CONTEXT_LENGTH },
                min: 0,
                step: 1000,
                placeholder: '上下文长度',
                style: 'width: 150px',
              }),
              ...CTX_PRESETS.map(p => h(NButton, {
                size: 'tiny',
                quaternary: true,
                type: opts.context_length === p.value ? 'primary' : 'default',
                onClick: () => { opts.context_length = p.value },
              }, { default: () => p.label })),
            ]),
            h(NInputNumber, {
              value: opts.max_output_length,
              'onUpdate:value': (v: number | null) => { opts.max_output_length = v ?? DEFAULT_MODEL_MAX_OUTPUT_LENGTH },
              min: 0,
              step: 1000,
              placeholder: '最大输出长度',
              style: 'width: 150px',
            }),
            h('div', { style: 'display: flex; align-items: center; gap: 6px' }, [
              h(NSwitch, {
                value: opts.multimodal,
                size: 'small',
                'onUpdate:value': (v: boolean) => { opts.multimodal = v },
              }),
              h('span', { style: 'font-size: 13px; color: var(--text-2)' }, '多模态'),
            ]),
            h(NButton, { type: 'primary', size: 'small', onClick: () => ctx.addM(id) }, { default: () => '添加' }),
            h(NButton, {
              size: 'small',
              loading: ctx.fetchingId.value === id,
              disabled: ctx.fetchingId.value !== null,
              onClick: () => ctx.fetchM(id),
            }, { default: () => '拉取模型' }),
          ]),
          models.length === 0
            ? h(NText, { depth: 3, style: 'font-size: 13px' }, { default: () => '暂无模型，可手动添加或点击「拉取模型」从上游获取' })
            : h('div', { style: 'display: flex; flex-wrap: wrap; gap: 8px' },
                models.map(m => h(NTag, {
                  type: m.manual ? 'default' : 'info',
                  bordered: false,
                  closable: true,
                  style: 'cursor: pointer',
                  onClick: (e: MouseEvent) => {
                    if ((e.target as HTMLElement).closest('.n-tag__close')) return
                    ctx.openModelEdit(id, m)
                  },
                  onClose: () => ctx.delM(id, m.id),
                }, { default: () => [
                  m.model_name + (m.manual ? '（手动）' : ''),
                  m.multimodal ? h('span', { style: 'opacity: 0.75; font-size: 11px; margin-left: 6px' }, '多模态') : null,
                  h('span', { style: 'opacity: 0.6; font-size: 11px; margin-left: 6px' }, `${fmtK(m.context_length)} / ${fmtK(m.max_output_length)}`),
                ] }))
              ),
        ])
      },
    },
    // 所有列宽都来自 colW（随容器宽度自适应）；文本列配 ellipsis + tooltip，
    // 收窄时截断而不是把表格撑出横向滚动条
    { title: '名称', key: 'name', width: ctx.colW.value.name, ellipsis: { tooltip: true }, render: (row) => h('span', { style: 'font-weight: 600; color: var(--text)' }, row.name) },
    // 备注紧跟名称：窄屏首屏就能看到，空值显示「—」
    {
      title: '备注',
      key: 'remark',
      width: ctx.colW.value.remark,
      ellipsis: { tooltip: true },
      render: (row) => (row.remark
        ? h('span', { style: 'color: var(--text-2)' }, row.remark)
        : h('span', { style: 'color: var(--text-4)' }, '—')),
    },
    // 标记（官方/中转）：快速分清中转站，纯展示（后端缓存/路由都不读它）。
    // 缺省/未知值一律显示「官方」，与后端 readTag 的容忍口径一致
    {
      title: '标记',
      key: 'tag',
      width: ctx.colW.value.tag,
      render: (row) => h(NTag, { type: tagTone(row.tag), bordered: false, size: 'small' }, { default: () => tagLabel(row.tag) }),
    },
    {
      title: '状态', key: 'enabled', width: ctx.colW.value.status, render: (row) => h(NSwitch, {
        value: row.enabled, size: 'small', 'onUpdate:value': (v: boolean) => ctx.toggleEnabled(row, v) }) },
    {
      title: '有效期至',
      key: 'expires_at',
      width: ctx.colW.value.expiry,
      render: (row) => {
        const { text, tone } = expiryLabel(row)
        if (tone === 'error') return h(NTag, { type: 'error', bordered: false, size: 'small' }, { default: () => text })
        if (tone === 'muted') return h('span', { style: 'color: var(--text-4)' }, text)
        return h('span', { style: 'font-size: 12.5px' }, text)
      },
    },
    { title: '地址', key: 'base_url', width: ctx.colW.value.baseUrl, ellipsis: { tooltip: true }, render: (row) => h('span', { class: 'mono', style: 'font-size: 12.5px' }, row.base_url) },
    {
      title: '格式',
      key: 'format',
      width: ctx.colW.value.format,
      // 主格式 + 附加端点格式各一个 tag；附加的半透明显示，主从一眼可辨
      render: (row) => h('div', { style: 'display: flex; gap: 4px; flex-wrap: wrap' }, [
        h(NTag, { type: row.format === 'openai' ? 'info' : 'warning', bordered: false, size: 'small' }, { default: () => row.format }),
        ...(row.extra_endpoints ?? []).map(e => h(NTag, {
          type: e.format === 'openai' ? 'info' : 'warning', bordered: false, size: 'small', style: 'opacity: 0.6',
        }, { default: () => e.format })),
      ]),
    },
    {
      title: '模型数',
      key: 'model_count',
      width: ctx.colW.value.modelCount,
      render: (row) => h('span', { class: 'mono' }, formatInt(row.model_count ?? 0)),
    },
    {
      title: '余额/额度',
      key: 'balance',
      width: ctx.colW.value.balance,
      render: (row) => {
        const s = ctx.balancesByUpstream.value[row.id as number]
        const v = s ? balanceView(s) : null
        if (!s || !v) return h('span', { style: 'color: var(--text-4)' }, '-')
        const dim = 'color: var(--text-4); font-size: 12px'
        const lines = v.kind === 'balance'
          ? [h('div', { class: 'mono' }, v.text)]
          : v.windows.map(w => h('div', { class: 'mono' }, w.missing
            ? [h('span', { style: 'color: var(--text-4)' }, `${w.label} —`)]
            : [h('span', null, `${w.label} ${w.percent}%`),
              ...(w.reset ? [h('span', { style: dim }, `（${w.reset} 重置）`)] : [])]))
        lines.push(h('div', { style: dim }, `更新于 ${formatFetchedAt(s.created_at)}`))
        return h('div', { style: 'cursor: pointer; line-height: 1.5', title: balanceTooltip(s), onClick: () => ctx.openHistory(row) }, lines)
      },
    },
    {
      title: '日 token 上限',
      key: 'daily_token_limit',
      width: ctx.colW.value.dailyLimit,
      render: (row) => row.daily_token_limit > 0
        ? h('span', { class: 'mono' }, formatInt(row.daily_token_limit))
        : h('span', { style: 'color: var(--text-4)' }, '不限'),
    },
    {
      title: '月 token 上限',
      key: 'monthly_token_limit',
      width: ctx.colW.value.monthlyLimit,
      render: (row) => row.monthly_token_limit > 0
        ? h('span', { class: 'mono' }, formatInt(row.monthly_token_limit))
        : h('span', { style: 'color: var(--text-4)' }, '不限'),
    },
    {
      title: '并发上限',
      key: 'max_concurrent',
      width: ctx.colW.value.maxConcurrent,
      render: (row) => row.max_concurrent > 0
        ? h('span', { class: 'mono' }, formatInt(row.max_concurrent))
        : h('span', { style: 'color: var(--text-4)' }, '不限'),
    },
    // 操作列宽来自 colW（压缩空间最大的一列），NSpace 允许换行——容器越窄
    // 按钮换行越多，表格本身永不出现横向滚动条
    { title: '操作', key: 'actions', width: ctx.colW.value.actions, render: (row) => h(NSpace, { size: 8, wrap: true }, {
      default: () => [
        h(NButton, { size: 'small', onClick: () => ctx.edit(row) }, { default: () => '编辑' }),
        h(NButton, {
          size: 'small',
          loading: ctx.testingId.value === row.id,
          disabled: ctx.testingId.value !== null,
          onClick: () => ctx.testRow(row),
        }, { default: () => '测试' }),
        h(NButton, {
          size: 'small',
          loading: ctx.fetchingId.value === row.id,
          disabled: ctx.fetchingId.value !== null,
          onClick: () => ctx.fetchM(row.id as number),
        }, { default: () => '拉取模型' }),
        h(NButton, {
          size: 'small',
          quaternary: true,
          loading: ctx.refreshingId.value === row.id,
          disabled: ctx.refreshingId.value !== null,
          onClick: () => ctx.refreshB(row.id as number),
        }, { default: () => '刷新余额' }),
        h(NButton, { size: 'small', quaternary: true, onClick: () => ctx.openHistory(row) }, { default: () => '历史' }),
        h(NPopconfirm, { onPositiveClick: () => ctx.del(row.id as number) }, {
          trigger: () => h(NButton, { size: 'small', type: 'error', quaternary: true }, { default: () => '删除' }),
          default: () => '确定删除？',
        }),
      ],
    }) },
  ])

  return { columns, historyColumns }
}
