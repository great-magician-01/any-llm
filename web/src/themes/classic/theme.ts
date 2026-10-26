import type { GlobalThemeOverrides } from 'naive-ui'
import { rgba, surfaceOf, themeById, themeNaiveOverrides, type ThemeId } from '@/utils/themes'

/**
 * 全局主题：经典版配色。由 App.vue 按 useTheme() 的 theme 调
 * darkThemeOverrides / lightThemeOverrides 取得。
 *
 * 主题注册表（utils/themes.ts）给了每个主题的品牌色与整套表面色：
 *   - 默认蓝（深色 / 浅色）不注入品牌覆盖，用本文件内置值；
 *   - 机体主题（初号机 / 明日香 / 凌波丽）的品牌色段与表面调色板由注册表提供，
 *     因此选中机体主题时连背景、卡片、浮层、描边一起换。
 * 浅色只存在于默认蓝主题，表面的浅色值仍写在本文件与 style.css 的 light 块里。
 */

type Common = NonNullable<GlobalThemeOverrides['common']>

/** 两套主题共用的品牌识别、圆角与字体（默认蓝的品牌色即下面的 primary 段）。 */
const commonShared = {
  primaryColor: '#5b8cff',
  primaryColorHover: '#7ba3ff',
  primaryColorPressed: '#4673e8',
  primaryColorSuppl: '#7ba3ff',
  infoColor: '#38bdf8',
  infoColorHover: '#5cc9fa',
  infoColorPressed: '#1ea2e4',
  infoColorSuppl: '#5cc9fa',
  successColor: '#34d399',
  successColorHover: '#4adea8',
  successColorPressed: '#26b886',
  successColorSuppl: '#4adea8',
  warningColor: '#fbbf24',
  warningColorHover: '#fccb4a',
  warningColorPressed: '#e5ab17',
  warningColorSuppl: '#fccb4a',
  errorColor: '#fb7185',
  errorColorHover: '#fc8a9b',
  errorColorPressed: '#e85c71',
  errorColorSuppl: '#fc8a9b',

  borderRadius: '10px',
  borderRadiusSmall: '6px',
  fontSize: '14px',
  fontFamily:
    "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif",
  fontFamilyMono: "ui-monospace, 'SF Mono', Consolas, 'Courier New', monospace",
} satisfies Common

/** common 段：默认蓝用内置值；机体主题展开注册表的品牌色覆盖（明日香含 error 族）。 */
function buildCommon(theme: ThemeId): Common {
  return { ...commonShared, ...themeNaiveOverrides(theme) }
}

/**
 * 菜单激活态：默认蓝用经典版内置蓝，机体主题的激活文字色 / 主色 rgb 由注册表提供，
 * 透明度沿用经典版的档位。
 */
function menuActive(theme: ThemeId, isDark: boolean) {
  const t = themeById(theme)
  const rgb = t.primary ? t.rgb : '91, 140, 255'
  return {
    text: t.primary ? t.menu : isDark ? '#8fb0ff' : '#3b6fe0',
    bg: `rgba(${rgb}, ${isDark ? 0.14 : 0.12})`,
    bgHover: `rgba(${rgb}, ${isDark ? 0.2 : 0.18})`,
  }
}

export function darkThemeOverrides(theme: ThemeId = 'dark'): GlobalThemeOverrides {
  const s = surfaceOf(theme)
  const menu = menuActive(theme, true)
  return {
    common: {
      ...buildCommon(theme),

      bodyColor: s.bg,
      cardColor: s.card,
      modalColor: s.modal,
      popoverColor: s.popover,
      tableColor: s.card,
      inputColor: s.input,
      actionColor: s.card,
      hoverColor: rgba(s.tint, 0.08),

      textColorBase: '#cbd5e1',
      textColor1: '#f1f5f9',
      textColor2: '#cbd5e1',
      textColor3: '#8fa0b8',
      placeholderColor: '#5b6b82',
      borderColor: rgba(s.tint, 0.18),
      dividerColor: rgba(s.tint, 0.12),
    },
    Layout: {
      color: s.bg,
      siderColor: s.sider,
    },
    Menu: {
      borderRadius: '10px',
      itemHeight: '42px',
      itemTextColor: '#8fa0b8',
      itemTextColorHover: '#e2e8f0',
      itemTextColorActive: menu.text,
      itemTextColorChildActive: menu.text,
      itemIconColor: '#6b7c98',
      itemIconColorHover: '#e2e8f0',
      itemIconColorActive: menu.text,
      itemIconColorChildActive: menu.text,
      itemColorHover: rgba(s.tint, 0.06),
      itemColorActive: menu.bg,
      itemColorActiveHover: menu.bgHover,
    },
    Card: {
      borderColor: rgba(s.tint, 0.1),
      borderRadius: '14px',
      titleFontWeight: '600',
      titleFontSizeMedium: '15px',
      paddingMedium: '18px 22px',
    },
    DataTable: {
      borderColor: rgba(s.tint, 0.1),
      thColor: rgba(s.tint, 0.05),
      thColorHover: rgba(s.tint, 0.09),
      tdColorHover: rgba(s.tint, 0.04),
      thTextColor: '#8fa0b8',
      tdTextColor: '#cbd5e1',
      thFontWeight: '600',
    },
    Button: {
      borderRadiusMedium: '8px',
      borderRadiusSmall: '6px',
      fontWeight: '500',
    },
    Input: {
      borderRadius: '8px',
    },
    Modal: {
      borderRadius: '14px',
    },
    Tag: {
      borderRadius: '6px',
    },
  }
}

/** 浅色主题只有默认蓝一档，品牌色沿用 commonShared，表面色是固定的冷白。 */
export function lightThemeOverrides(): GlobalThemeOverrides {
  const menu = menuActive('light', false)
  return {
    common: {
      ...commonShared,

      bodyColor: '#f4f6fb',
      cardColor: '#ffffff',
      modalColor: '#ffffff',
      popoverColor: '#ffffff',
      tableColor: '#ffffff',
      inputColor: '#ffffff',
      actionColor: '#eef1f7',
      hoverColor: 'rgba(15, 23, 42, 0.05)',

      textColorBase: '#334155',
      textColor1: '#0f172a',
      textColor2: '#334155',
      textColor3: '#64748b',
      placeholderColor: '#94a3b8',
      borderColor: 'rgba(15, 23, 42, 0.12)',
      dividerColor: 'rgba(15, 23, 42, 0.08)',
    },
    Layout: {
      color: '#f4f6fb',
      siderColor: '#ffffff',
    },
    Menu: {
      borderRadius: '10px',
      itemHeight: '42px',
      itemTextColor: '#64748b',
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
      borderRadius: '14px',
      titleFontWeight: '600',
      titleFontSizeMedium: '15px',
      paddingMedium: '18px 22px',
    },
    DataTable: {
      borderColor: 'rgba(15, 23, 42, 0.1)',
      thColor: '#f1f5fb',
      thColorHover: 'rgba(15, 23, 42, 0.05)',
      tdColorHover: 'rgba(15, 23, 42, 0.03)',
      thTextColor: '#64748b',
      tdTextColor: '#334155',
      thFontWeight: '600',
    },
    Button: {
      borderRadiusMedium: '8px',
      borderRadiusSmall: '6px',
      fontWeight: '500',
    },
    Input: {
      borderRadius: '8px',
    },
    Modal: {
      borderRadius: '14px',
    },
    Tag: {
      borderRadius: '6px',
    },
  }
}
