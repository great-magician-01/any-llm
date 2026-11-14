/**
 * 模型别名多配置（profile）的纯函数：管理端把扁平的绑定列表按配置分组编辑，
 * 保存时再按配置顺序摊平回「全局 priority = 数组顺序」的接口结构。
 *
 * 后端约定：profile 空串归一为 default；同一别名下每套配置是一条独立绑定链，
 * 只有 active_profile 指向的那套会被网关解析（见 internal/store/alias.go）。
 */
import type { AliasBindingInput } from '@/api/aliases'

export const DEFAULT_ALIAS_PROFILE = 'default'

export interface ProfileBindingInput {
  upstream_id: number | null
  model_name: string
}

export interface ProfileForm {
  name: string
  bindings: ProfileBindingInput[]
}

export interface ProfileGroup<T> {
  name: string
  bindings: T[]
}

/** 界面展示名：default 显示为「默认」，其它配置名原样显示。 */
export function profileLabel(name: string): string {
  return name === DEFAULT_ALIAS_PROFILE ? '默认' : name
}

/** 配置名归一：裁剪空白，空串落 default（与后端 NormalizeAliasProfile 对齐）。 */
export function normalizeProfileName(name: string): string {
  return name.trim() || DEFAULT_ALIAS_PROFILE
}

/**
 * 按 profile 分组：组顺序按首次出现（绑定已按 priority 排序），组内保持原顺序。
 * 空 profile 归 default。用于把接口返回的扁平绑定还原成编辑页的配置页签。
 */
export function groupBindingsByProfile<T extends { profile?: string }>(bindings: T[]): ProfileGroup<T>[] {
  const groups: ProfileGroup<T>[] = []
  const byName = new Map<string, ProfileGroup<T>>()
  for (const b of bindings) {
    const name = normalizeProfileName(b.profile ?? '')
    let g = byName.get(name)
    if (!g) {
      g = { name, bindings: [] }
      byName.set(name, g)
      groups.push(g)
    }
    g.bindings.push(b)
  }
  return groups
}

/**
 * 按配置顺序摊平绑定列表。priority 由后端按数组顺序重写，因此跨配置的先后
 * 无所谓，只要每套配置内部的相对顺序不变即可（group 时已保持）。
 */
export function flattenAliasProfiles(profiles: ProfileForm[]): AliasBindingInput[] {
  const out: AliasBindingInput[] = []
  for (const p of profiles) {
    const profile = normalizeProfileName(p.name)
    for (const b of p.bindings) {
      if (b.upstream_id == null) continue
      out.push({ upstream_id: b.upstream_id, model_name: b.model_name.trim(), profile })
    }
  }
  return out
}

/** 新配置的默认名：避免与现有配置重名（新配置、新配置2、新配置3 …）。 */
export function uniqueProfileName(existing: string[], base = '新配置'): string {
  if (!existing.includes(base)) return base
  for (let i = 2; ; i++) {
    const name = `${base}${i}`
    if (!existing.includes(name)) return name
  }
}

/** 生效配置兜底：请求值仍在配置列表里就保留，否则取第一套（列表为空时 default）。 */
export function resolveActiveProfile(active: string, profiles: ProfileForm[]): string {
  const names = profiles.map((p) => normalizeProfileName(p.name))
  return names.includes(active) ? active : names[0] || DEFAULT_ALIAS_PROFILE
}
