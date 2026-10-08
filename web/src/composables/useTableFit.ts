import { onScopeDispose, readonly, ref, watch, type Ref } from 'vue'

/**
 * 上游管理页表格的连续自适应（两套皮肤共用）。
 *
 * 旧方案是「宽/窄」两档：视口窄于 1400px 才把操作列从 440 收到 250。但布局
 * 内容区上限约 1486px（侧栏 228 + .page max-width 1600 + padding + 卡片内边距），
 * 而列宽总和（UPSTREAM_FIT_COLUMNS 的 wide 之和）大于它——任何屏幕下表格都比
 * 容器宽，横向滚动条常驻，两档断点根本救不回来。
 *
 * 现在的做法：用 ResizeObserver 量出表格容器的实际宽度，每列从理想宽度（wide）
 * 按比例压缩到各自下限（min）。压缩量按各列的「可压缩空间」（wide − min）分配，
 * 操作列的可压缩空间最大（440→128），所以它缩得最快——NSpace 允许换行，按钮
 * 随列宽收窄自然从一行变成两行、三行，其余列靠 ellipsis/tooltip 消化收窄。
 * scroll-x 始终取 min(列宽总和, 容器宽)，因此任何屏宽都不会出现横向滚动条；
 * 容器比所有列的下限总和还窄时，fixed 布局会把各列在下限之下再等比压缩
 * （可读性让位于「一屏展示全部内容」的约定）。
 *
 * 断点、档位、列宽规格全部收敛在这一个文件里——两套皮肤各写一份就会悄悄漂移。
 */

/** 一列的自适应规格：wide 理想宽度，min 压缩下限（min === wide 表示不参与压缩）。 */
export interface FitColumn {
  key: string
  wide: number
  min: number
}

/**
 * 上游列表的列宽规格。min 的取值依据：
 *   - expand：展开箭头不可压缩
 *   - 文本列（名称/备注/地址）：有 ellipsis + tooltip，压到下限只是截断更多
 *   - expiry：「2026-10-20 21:59:53」压不下时日期与时间折成两行（span 可换行）
 *   - format：下限按「anthropic」标签的实际宽度定
 *   - actions：6 个 small 按钮一行约需 440（含 NSpace 间距与单元格内边距）；
 *     下限 128 约等于一个最宽按钮（拉取模型/刷新余额）占一行
 */
export const UPSTREAM_FIT_COLUMNS: readonly FitColumn[] = [
  { key: 'expand', wide: 40, min: 40 },
  { key: 'name', wide: 130, min: 88 },
  { key: 'remark', wide: 150, min: 72 },
  { key: 'tag', wide: 92, min: 72 },
  { key: 'status', wide: 80, min: 60 },
  { key: 'expiry', wide: 150, min: 110 },
  { key: 'baseUrl', wide: 180, min: 120 },
  { key: 'format', wide: 110, min: 92 },
  { key: 'modelCount', wide: 80, min: 60 },
  { key: 'balance', wide: 180, min: 140 },
  { key: 'dailyLimit', wide: 120, min: 84 },
  { key: 'monthlyLimit', wide: 120, min: 84 },
  { key: 'maxConcurrent', wide: 100, min: 72 },
  { key: 'actions', wide: 440, min: 128 },
]

/**
 * 按容器宽度拟合各列宽度。
 *
 * 容器宽度未知（<= 0，如测试环境没布局）或足够宽时全部取理想宽度；否则把缺口
 * （理想总和 − 容器宽）按各列可压缩空间的比例分摊，每列向下取整——保证
 * Σ返回值 <= min(容器宽, 理想总和)，配合 fitScrollX 不会撑出横向滚动条。
 * 缺口超过总压缩能力时比例钳到 1，各列停在下限（更窄的容器交给 fixed 布局
 * 等比压缩，见文件头注释）。
 */
export function fitColumns(containerWidth: number, cols: readonly FitColumn[]): Record<string, number> {
  const total = cols.reduce((s, c) => s + c.wide, 0)
  const out: Record<string, number> = {}
  if (containerWidth <= 0 || containerWidth >= total) {
    for (const c of cols) out[c.key] = c.wide
    return out
  }
  const deficit = total - containerWidth
  const capacity = cols.reduce((s, c) => s + (c.wide - c.min), 0)
  const ratio = Math.min(1, deficit / capacity)
  for (const c of cols) out[c.key] = Math.floor(c.wide - (c.wide - c.min) * ratio)
  return out
}

/**
 * 表格的 scroll-x 取值：列宽总和，但绝不超过容器宽度。
 *
 * scroll-x 在 naive-ui 里是表格的 min-width——大于容器就出横向滚动条；小于
 * 列宽总和时 fixed 布局会把各列等比压缩（容器窄于所有列的下限总和时正是靠
 * 这一点兜底的）。容器宽度未知（<= 0）时退化为列宽总和，维持旧的宽屏行为。
 */
export function fitScrollX(widths: Record<string, number>, containerWidth: number): number {
  const sum = Object.values(widths).reduce((a, b) => a + b, 0)
  return containerWidth > 0 ? Math.min(sum, Math.floor(containerWidth)) : sum
}

/**
 * 持续观测一个元素的宽度（内容盒，像素，向下取整）。
 *
 * 首选 ResizeObserver（窗口缩放、侧栏开合、卡片重排都能感知）；环境里没有
 * RO 时退回 window resize（happy-dom 等测试环境量不出布局，宽度保持 0，
 * fitColumns 按理想宽度渲染）。初始值同步量一次，首帧就是对的。
 */
export function useContainerWidth(el: Ref<HTMLElement | null>) {
  const width = ref(0)

  function measure() {
    const w = el.value?.getBoundingClientRect().width ?? 0
    if (w > 0) width.value = Math.floor(w)
  }

  // flush: 'post' 等模板 ref 落位后再量；watch 的 onCleanup 负责换元素/停止时解绑
  const stop = watch(el, (node, _prev, onCleanup) => {
    if (!node) return
    measure()
    if (typeof ResizeObserver !== 'undefined') {
      const ro = new ResizeObserver((entries) => {
        const w = entries[0]?.contentRect.width ?? 0
        if (w > 0) width.value = Math.floor(w)
      })
      ro.observe(node)
      onCleanup(() => ro.disconnect())
    } else if (typeof window !== 'undefined') {
      window.addEventListener('resize', measure)
      onCleanup(() => window.removeEventListener('resize', measure))
    }
  }, { immediate: true, flush: 'post' })

  // 组件卸载（或 effect scope 失效）时停止观测：这两个页面会反复进出
  onScopeDispose(() => stop())

  return { width: readonly(width) }
}
