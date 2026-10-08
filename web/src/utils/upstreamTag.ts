/**
 * 上游标记（官方 / 中转）的展示口径。
 *
 * 取值与后端 store.TagOfficial / store.TagRelay 对应：official = 官方源站，
 * relay = 中转站，缺省/未知值一律按默认的「官方」显示。纯管理端元数据——
 * 网关路由、转发不读它，所以这里只负责「怎么显示」，不做任何可达性判断。
 *
 * 两套皮肤（classic / glass）共用这一份：表单选项与列表标签都要用它，
 * 各写一份就会悄悄漂移（双主题 parity 测试只比页面文案，兜不住取值映射）。
 */

export type UpstreamTag = 'official' | 'relay'

/** 标记 → 展示文案。表单选项与列表标签都从这里取。 */
export const UPSTREAM_TAG_LABELS: Record<UpstreamTag, string> = {
  official: '官方',
  relay: '中转',
}

/** 表单用的下拉/单选选项（顺序即展示顺序：默认值在前）。 */
export const UPSTREAM_TAG_OPTIONS: readonly { label: string; value: UpstreamTag }[] = [
  { label: UPSTREAM_TAG_LABELS.official, value: 'official' },
  { label: UPSTREAM_TAG_LABELS.relay, value: 'relay' },
]

/** 缺省（旧数据/未填/脏值）按默认的「官方」显示，与后端 readTag 的容忍口径一致。 */
export function tagLabel(tag?: string | null): string {
  return (tag && UPSTREAM_TAG_LABELS[tag as UpstreamTag]) || UPSTREAM_TAG_LABELS.official
}

/** 列表里标记 tag 的配色：官方=info，中转=warning（一眼分出中转站）。 */
export function tagTone(tag?: string | null): 'info' | 'warning' {
  return tag === 'relay' ? 'warning' : 'info'
}
