import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * 毛玻璃主题：在深色底上把卡片 / 弹窗 / 表格等表面改为半透明白色叠加，
 * 配合 glass.css 中的 backdrop-filter 与极光背景使用（仅作用于 /glass 路由树）。
 */
export const glassThemeOverrides: GlobalThemeOverrides = {
  common: {
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

    borderRadius: '12px',
    borderRadiusSmall: '7px',
    fontSize: '14px',
    fontFamily:
      "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif",
    fontFamilyMono: "ui-monospace, 'SF Mono', Consolas, 'Courier New', monospace",
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
