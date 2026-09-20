import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * 毛玻璃主题：在底色上把卡片 / 弹窗 / 表格等表面改为半透明白色叠加，
 * 配合 glass.css 中的 backdrop-filter 与极光背景使用（仅作用于 /glass 路由树）。
 * 明暗两套由 GlassShell.vue 按 useTheme() 的 isDark 切换，品牌色共用 commonShared。
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

  borderRadius: '12px',
  borderRadiusSmall: '7px',
  fontSize: '14px',
  fontFamily:
    "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif",
  fontFamilyMono: "ui-monospace, 'SF Mono', Consolas, 'Courier New', monospace",
} satisfies Common

export const darkGlassThemeOverrides: GlobalThemeOverrides = {
  common: {
    ...commonShared,

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
    itemTextColorActive: '#b9cdff',
    itemTextColorChildActive: '#b9cdff',
    itemIconColor: '#7d8fae',
    itemIconColorHover: '#f0f5ff',
    itemIconColorActive: '#b9cdff',
    itemIconColorChildActive: '#b9cdff',
    itemColorHover: 'rgba(255, 255, 255, 0.07)',
    itemColorActive: 'rgba(123, 163, 255, 0.18)',
    itemColorActiveHover: 'rgba(123, 163, 255, 0.26)',
  },
  Card: {
    borderColor: 'rgba(255, 255, 255, 0.12)',
    borderRadius: '16px',
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
    borderRadiusMedium: '9px',
    borderRadiusSmall: '7px',
    fontWeight: '500',
  },
  Input: {
    borderRadius: '9px',
  },
  Modal: {
    borderRadius: '16px',
  },
  Tag: {
    borderRadius: '6px',
  },
  Dialog: {
    color: 'rgba(18, 24, 42, 0.62)',
  },
}

export const lightGlassThemeOverrides: GlobalThemeOverrides = {
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
    /** 激活态文字用深一档的品牌蓝，#b9cdff 在白底上对比度不够 */
    itemTextColor: '#5b6b85',
    itemTextColorHover: '#0f172a',
    itemTextColorActive: '#3560cf',
    itemTextColorChildActive: '#3560cf',
    itemIconColor: '#94a3b8',
    itemIconColorHover: '#0f172a',
    itemIconColorActive: '#3560cf',
    itemIconColorChildActive: '#3560cf',
    itemColorHover: 'rgba(15, 23, 42, 0.05)',
    itemColorActive: 'rgba(91, 140, 255, 0.14)',
    itemColorActiveHover: 'rgba(91, 140, 255, 0.2)',
  },
  Card: {
    borderColor: 'rgba(15, 23, 42, 0.1)',
    borderRadius: '16px',
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
    borderRadiusMedium: '9px',
    borderRadiusSmall: '7px',
    fontWeight: '500',
  },
  Input: {
    borderRadius: '9px',
  },
  Modal: {
    borderRadius: '16px',
  },
  Tag: {
    borderRadius: '6px',
  },
  Dialog: {
    color: 'rgba(255, 255, 255, 0.8)',
  },
}
