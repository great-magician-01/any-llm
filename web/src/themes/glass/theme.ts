import type { GlobalThemeOverrides } from 'naive-ui'
import { themeById, themeNaiveOverrides, type ThemeId } from '@/utils/themes'

/**
 * 液态玻璃主题：在底色上把卡片 / 弹窗 / 表格等表面改为渐变色叠加，
 * 配合 glass.css 中的 backdrop-filter、折射亮边与流光背景使用（仅作用于 /glass 路由树）。
 * 由 GlassShell.vue 按 useTheme() 的 theme 取得。
 *
 * 表面材质（半透明白 + 模糊）对所有暗色主题一致，机体主题的差异来自 glass.css
 * 里按主题定义的流光背景；品牌色段由注册表注入，与经典版共用同一组值。
 * 默认蓝的玻璃蓝比经典版亮一档，仍内置在本文件；浅色只有默认蓝一档。
 */

type Common = NonNullable<GlobalThemeOverrides['common']>

const commonShared = {
  primaryColor: '#7ba3ff',
  primaryColorHover: '#95b7ff',
  primaryColorPressed: '#5b8cff',
  primaryColorSuppl: '#95b7ff',
  infoColor: '#4ed8f0',
  infoColorHover: '#72e2f5',
  infoColorPressed: '#22d3ee',
  infoColorSuppl: '#72e2f5',
  successColor: '#4adea8',
  successColorHover: '#6fe7ba',
  successColorPressed: '#34d399',
  successColorSuppl: '#6fe7ba',
  warningColor: '#fccb4a',
  warningColorHover: '#fdd86e',
  warningColorPressed: '#fbbf24',
  warningColorSuppl: '#fdd86e',
  errorColor: '#fc8a9b',
  errorColorHover: '#fda5b2',
  errorColorPressed: '#fb7185',
  errorColorSuppl: '#fda5b2',

  borderRadius: '14px',
  borderRadiusSmall: '8px',
  fontSize: '14px',
  fontFamily:
    "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif",
  fontFamilyMono: "ui-monospace, 'SF Mono', Consolas, 'Courier New', monospace",
} satisfies Common

/** common 段：默认蓝用玻璃蓝内置值；机体主题展开注册表的品牌色覆盖。 */
function buildCommon(theme: ThemeId): Common {
  return { ...commonShared, ...themeNaiveOverrides(theme) }
}

/**
 * 菜单激活态：默认蓝用玻璃版内置值（激活底色透明度比经典版高一档），
 * 机体主题的激活文字色 / 主色 rgb 由注册表提供，透明度沿用本套的档位。
 */
function menuActive(theme: ThemeId, isDark: boolean) {
  const t = themeById(theme)
  const rgb = t.primary ? t.rgb : '123, 163, 255'
  return {
    text: t.primary ? t.menu : isDark ? '#b9cdff' : '#3560cf',
    bg: `rgba(${rgb}, ${isDark ? 0.18 : 0.14})`,
    bgHover: `rgba(${rgb}, ${isDark ? 0.26 : 0.2})`,
  }
}

export function darkGlassThemeOverrides(theme: ThemeId = 'dark'): GlobalThemeOverrides {
  const menu = menuActive(theme, true)
  return {
    common: {
      ...buildCommon(theme),

      bodyColor: 'transparent',
      cardColor: 'rgba(255, 255, 255, 0.07)',
      modalColor: 'rgba(18, 24, 42, 0.62)',
      popoverColor: 'rgba(22, 29, 50, 0.68)',
      tableColor: 'rgba(255, 255, 255, 0.04)',
      inputColor: 'rgba(255, 255, 255, 0.06)',
      actionColor: 'rgba(255, 255, 255, 0.05)',
      hoverColor: 'rgba(255, 255, 255, 0.09)',

      textColorBase: '#dbe4f3',
      textColor1: '#f5f8ff',
      textColor2: '#dbe4f3',
      textColor3: '#9fb0c9',
      placeholderColor: '#687992',
      borderColor: 'rgba(255, 255, 255, 0.16)',
      dividerColor: 'rgba(255, 255, 255, 0.1)',
    },
    Layout: {
      color: 'transparent',
      siderColor: 'transparent',
    },
    Menu: {
      borderRadius: '10px',
      itemHeight: '42px',
      itemTextColor: '#9fb0c9',
      itemTextColorHover: '#f0f5ff',
      itemTextColorActive: menu.text,
      itemTextColorChildActive: menu.text,
      itemIconColor: '#7d8fae',
      itemIconColorHover: '#f0f5ff',
      itemIconColorActive: menu.text,
      itemIconColorChildActive: menu.text,
      itemColorHover: 'rgba(255, 255, 255, 0.07)',
      itemColorActive: menu.bg,
      itemColorActiveHover: menu.bgHover,
    },
    Card: {
      borderColor: 'rgba(255, 255, 255, 0.16)',
      borderRadius: '20px',
      titleFontWeight: '600',
      titleFontSizeMedium: '15px',
      paddingMedium: '18px 22px',
    },
    DataTable: {
      borderColor: 'rgba(255, 255, 255, 0.1)',
      thColor: 'rgba(255, 255, 255, 0.05)',
      thColorHover: 'rgba(255, 255, 255, 0.09)',
      tdColor: 'transparent',
      tdColorHover: 'rgba(255, 255, 255, 0.05)',
      thTextColor: '#9fb0c9',
      tdTextColor: '#dbe4f3',
      thFontWeight: '600',
    },
    Button: {
      borderRadiusMedium: '10px',
      borderRadiusSmall: '8px',
      fontWeight: '500',
    },
    Input: {
      borderRadius: '10px',
    },
    Modal: {
      borderRadius: '20px',
    },
    Tag: {
      borderRadius: '7px',
    },
    Dialog: {
      color: 'rgba(18, 24, 42, 0.62)',
    },
  }
}

/** 浅色玻璃只有默认蓝一档，材质换成白色液态渐变（见 glass.css 的 light 块）。 */
export function lightGlassThemeOverrides(): GlobalThemeOverrides {
  const menu = menuActive('light', false)
  return {
    common: {
      ...commonShared,

      bodyColor: 'transparent',
      cardColor: 'rgba(255, 255, 255, 0.62)',
      modalColor: 'rgba(255, 255, 255, 0.8)',
      popoverColor: 'rgba(255, 255, 255, 0.82)',
      tableColor: 'rgba(255, 255, 255, 0.45)',
      inputColor: 'rgba(255, 255, 255, 0.6)',
      actionColor: 'rgba(255, 255, 255, 0.5)',
      hoverColor: 'rgba(15, 23, 42, 0.05)',

      textColorBase: '#1e293b',
      textColor1: '#0f172a',
      textColor2: '#1e293b',
      textColor3: '#5b6b85',
      placeholderColor: '#94a3b8',
      borderColor: 'rgba(15, 23, 42, 0.1)',
      dividerColor: 'rgba(15, 23, 42, 0.07)',
    },
    Layout: {
      color: 'transparent',
      siderColor: 'transparent',
    },
    Menu: {
      borderRadius: '10px',
      itemHeight: '42px',
      itemTextColor: '#5b6b85',
      itemTextColorHover: '#0f172a',
      itemTextColorActive: menu.text,
      itemTextColorChildActive: menu.text,
      itemIconColor: '#94a3b8',
      itemIconColorHover: '#0f172a',
      itemIconColorActive: menu.text,
      itemIconColorChildActive: menu.text,
      itemColorHover: 'rgba(15, 23, 42, 0.05)',
      itemColorActive: menu.bg,
      itemColorActiveHover: menu.bgHover,
    },
    Card: {
      borderColor: 'rgba(15, 23, 42, 0.1)',
      borderRadius: '20px',
      titleFontWeight: '600',
      titleFontSizeMedium: '15px',
      paddingMedium: '18px 22px',
    },
    DataTable: {
      borderColor: 'rgba(15, 23, 42, 0.08)',
      thColor: 'rgba(15, 23, 42, 0.04)',
      thColorHover: 'rgba(15, 23, 42, 0.06)',
      tdColor: 'transparent',
      tdColorHover: 'rgba(15, 23, 42, 0.03)',
      thTextColor: '#5b6b85',
      tdTextColor: '#1e293b',
      thFontWeight: '600',
    },
    Button: {
      borderRadiusMedium: '10px',
      borderRadiusSmall: '8px',
      fontWeight: '500',
    },
    Input: {
      borderRadius: '10px',
    },
    Modal: {
      borderRadius: '20px',
    },
    Tag: {
      borderRadius: '7px',
    },
    Dialog: {
      color: 'rgba(255, 255, 255, 0.8)',
    },
  }
}
