/**
 * 双主题 parity：两套「API 密钥」页的客户端配置导出器必须一一对应。
 *
 * 两套皮肤逐行重复（classic/views/Keys.vue 与 glass/views/GlassKeys.vue），
 * 历史上这种重复漂移过（见 Dashboard.test.ts 的 useClipboard 前车之鉴）。
 * 这里做源码级 parity：从两份 SFC 源码里抽出 build…/copy… 导出器标识符，
 * 断言集合完全相同 —— 新增/删除一个客户端导出（如 pi）只改一边就会失败。
 */
import { describe, expect, it } from 'vitest'
// ?raw 直接取 SFC 源码做源码级 parity 断言（vite/client 已声明 *?raw 模块）
import CLASSIC_SRC from './classic/views/Keys.vue?raw'
import GLASS_SRC from './glass/views/GlassKeys.vue?raw'

/** 抽出源码里的导出器标识符（buildXxxConfig / copyXxxConfig / openXxxExport） */
function exportersOf(src: string): string[] {
  return [...new Set(src.match(/\b(?:build|copy)[A-Z]\w*Config\b|\bopen[A-Z]\w*Export\b/g) ?? [])].sort()
}

describe('双主题 parity：Keys 页客户端配置导出', () => {
  it('两套皮肤接入完全相同的导出器集合（防止只改一边）', () => {
    const classic = exportersOf(CLASSIC_SRC)
    // 断言非空，避免正则失配时两边都空、测试假通过
    expect(classic.length).toBeGreaterThan(0)
    expect(exportersOf(GLASS_SRC)).toEqual(classic)
  })

  it('两套皮肤都接入 pi 导出器（生成器 + 复制入口）', () => {
    for (const [name, src] of [['classic', CLASSIC_SRC], ['glass', GLASS_SRC]] as const) {
      expect(src, `${name} 必须引入 pi 生成器`).toContain("import { buildPiJson } from '@/utils/piConfig'")
      expect(src, `${name} 必须调用 buildPiJson`).toContain('buildPiJson({')
      expect(src, `${name} 必须在行内按钮与新建弹窗都提供 pi 入口`).toContain('copyPiConfig(newlyCreatedKey')
    }
  })

  it('两套皮肤都接入 Agent 配置文档抽屉（文档 + 按选中密钥生成）', () => {
    for (const [name, src] of [['classic', CLASSIC_SRC], ['glass', GLASS_SRC]] as const) {
      expect(src, `${name} 必须引入 AgentConfigDrawer`).toContain("import AgentConfigDrawer from '@/components/AgentConfigDrawer.vue'")
      expect(src, `${name} 必须渲染 AgentConfigDrawer 并传入密钥列表`).toContain('<AgentConfigDrawer v-model:show="showAgent" :keys="keys" />')
      expect(src, `${name} 页头必须提供 Agent 配置入口`).toContain('Agent 配置')
    }
  })
})
