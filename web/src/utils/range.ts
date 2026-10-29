/** 日期范围快捷档（Usage/Bills 两页共用）：全部走 Date 构造器做日历日运算，
 * 不用毫秒减法——固定 24h 跨夏令时会偏一天（拨快日窗口少一小时）。 */

/** 本月 1 号 00:00 → 今天 00:00（闭区间两端） */
export function currentMonthRange(): [number, number] {
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  return [new Date(today.getFullYear(), today.getMonth(), 1).getTime(), today.getTime()]
}

/** 含今天在内的最近 n 个日历日（今天往前数 n-1 天） */
export function lastNDays(n: number): [number, number] {
  const start = new Date()
  start.setHours(0, 0, 0, 0)
  start.setDate(start.getDate() - (n - 1))
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  return [start.getTime(), today.getTime()]
}

/** 今年 1 月 1 号 00:00 → 今天 00:00 */
export function currentYear(): [number, number] {
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  return [new Date(today.getFullYear(), 0, 1).getTime(), today.getTime()]
}
