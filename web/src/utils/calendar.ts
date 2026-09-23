/** GitHub 风格贡献日历的网格构建与色阶分桶（概览页 UsageCalendar 用） */

/** 后端 UsageDayStat 中本工具依赖的最小字段集（结构子集，直接传入即可） */
export interface CalendarDayInput {
  day: string
  total_tokens: number
  request_count: number
  error_count: number
}

export interface CalendarCell {
  /** 本地日期 "YYYY-MM-DD" */
  date: string
  /** 列（周）与行（周日=0 … 周六=6） */
  col: number
  row: number
  tokens: number
  requests: number
  errors: number
  /** 色阶 0–4（0 = 无用量） */
  level: number
}

export interface CalendarGrid {
  cells: CalendarCell[]
  cols: number
  /** 月份标签：text 渲染在 col 列上方 */
  monthLabels: { col: number; text: string }[]
}

function dayKey(d: Date): string {
  const p = (x: number) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/** 色阶：0 = 无用量；1–4 按当日 token 占窗口峰值的比例四等分（>0–25% … >75%） */
export function levelFor(tokens: number, max: number): number {
  if (tokens <= 0 || max <= 0) return 0
  return Math.min(4, Math.ceil((tokens / max) * 4))
}

/**
 * 以 today 为最后一格、列对齐到周日（GitHub 贡献图布局）构建网格，
 * 共 53 列（364 天 + 回退到周日多出的几天）。stats 缺失的日期按 0 处理；
 * today 之后的格子不生成。stats 的 day 取前 10 个字符分桶——后端发出的是
 * 服务器本地午夜的 RFC3339，与浏览器时区无关，不能用 new Date 换算。
 */
export function buildCalendarGrid(stats: CalendarDayInput[], today = new Date()): CalendarGrid {
  const byDay = new Map<string, CalendarDayInput>()
  for (const s of stats) byDay.set(s.day.slice(0, 10), s)

  const end = new Date(today.getFullYear(), today.getMonth(), today.getDate())
  const start = new Date(end)
  start.setDate(start.getDate() - 52 * 7)
  start.setDate(start.getDate() - start.getDay()) // 回退到本周周日

  const days: Date[] = []
  for (const d = new Date(start); d <= end; d.setDate(d.getDate() + 1)) {
    days.push(new Date(d))
  }

  let max = 0
  const raw = days.map((d, i) => {
    const key = dayKey(d)
    const s = byDay.get(key)
    const tokens = s?.total_tokens ?? 0
    if (tokens > max) max = tokens
    return {
      date: key,
      col: Math.floor(i / 7),
      row: d.getDay(),
      tokens,
      requests: s?.request_count ?? 0,
      errors: s?.error_count ?? 0,
    }
  })
  const cells: CalendarCell[] = raw.map((c) => ({ ...c, level: levelFor(c.tokens, max) }))
  const cols = raw.length ? raw[raw.length - 1].col + 1 : 0

  // 月份标签：1 号落在哪一列就标在哪一列；首列若不含 1 号，用首格月份补一个
  const monthLabels: { col: number; text: string }[] = []
  for (let col = 0; col < cols; col++) {
    const colCells = cells.filter((c) => c.col === col)
    if (!colCells.length) continue
    const first = colCells.find((c) => c.date.endsWith('-01'))
    if (first) {
      monthLabels.push({ col, text: `${Number(first.date.slice(5, 7))}月` })
    } else if (col === 0) {
      monthLabels.push({ col: 0, text: `${Number(colCells[0].date.slice(5, 7))}月` })
    }
  }

  return { cells, cols, monthLabels }
}
