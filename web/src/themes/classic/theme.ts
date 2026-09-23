import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * 全局主题：经典版配色（深空暗色 navy / 浅色冷白），由 App.vue 按
 * useTheme() 的 isDark 在 darkThemeOverrides / lightThemeOverrides 之间切换。
 * 两套仅表面 / 文字 / 边框不同，品牌色与排版共用 commonShared。
 */

type Common = NonNullable<GlobalThemeOverrides['common']>

/** 两套主题共用的品牌色、圆角与字体——品牌识别不随明暗变化。 */
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

export const darkThemeOverrides: GlobalThemeOverrides = {
  common: {
    ...commonShared,

    bodyColor: '#070b14',
    cardColor: '#0e1526',
    modalColor: '#101828',
    popoverColor: '#141d33',
    tableColor: '#0e1526',
    inputColor: '#0a101f',
    actionColor: '#0e1526',
    hoverColor: 'rgba(148, 163, 184, 0.08)',

    textColorBase: '#cbd5e1',
    textColor1: '#f1f5f9',
    textColor2: '#cbd5e1',
    textColor3: '#8fa0b8',
    placeholderColor: '#5b6b82',
    borderColor: 'rgba(148, 163, 184, 0.18)',
    dividerColor: 'rgba(148, 163, 184, 0.12)',
  },
  Layout: {
    color: '#070b14',
    siderColor: '#0a0f1d',
  },
  Menu: {
    borderRadius: '10px',
    itemHeight: '42px',
    itemTextColor: '#8fa0b8',
    itemTextColorHover: '#e2e8f0',
    itemTextColorActive: '#8fb0ff',
    itemTextColorChildActive: '#8fb0ff',
    itemIconColor: '#6b7c98',
    itemIconColorHover: '#e2e8f0',
    itemIconColorActive: '#8fb0ff',
    itemIconColorChildActive: '#8fb0ff',
    itemColorHover: 'rgba(148, 163, 184, 0.06)',
    itemColorActive: 'rgba(91, 140, 255, 0.14)',
    itemColorActiveHover: 'rgba(91, 140, 255, 0.2)',
  },
  Card: {
    borderColor: 'rgba(148, 163, 184, 0.1)',
    borderRadius: '14px',
    titleFontWeight: '600',
    titleFontSizeMedium: '15px',
    paddingMedium: '18px 22px',
  },
  DataTable: {
    borderColor: 'rgba(148, 163, 184, 0.1)',
    thColor: 'rgba(148, 163, 184, 0.05)',
    thColorHover: 'rgba(148, 163, 184, 0.09)',
    tdColorHover: 'rgba(148, 163, 184, 0.04)',
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

export const lightThemeOverrides: GlobalThemeOverrides = {
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
    /** 激活态文字用深一档的品牌蓝，#8fb0ff 在白底上对比度不够 */
    itemTextColor: '#64748b',
    itemTextColorHover: '#0f172a',
    itemTextColorActive: '#3b6fe0',
    itemTextColorChildActive: '#3b6fe0',
    itemIconColor: '#94a3b8',
    itemIconColorHover: '#0f172a',
    itemIconColorActive: '#3b6fe0',
    itemIconColorChildActive: '#3b6fe0',
    itemColorHover: 'rgba(15, 23, 42, 0.05)',
    itemColorActive: 'rgba(91, 140, 255, 0.12)',
    itemColorActiveHover: 'rgba(91, 140, 255, 0.18)',
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
