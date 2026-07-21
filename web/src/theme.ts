import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * 全局主题：深空暗色（navy / 蓝青渐变点缀），配合 App.vue 中的 darkTheme 使用。
 */
export const themeOverrides: GlobalThemeOverrides = {
  common: {
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

    borderRadius: '10px',
    borderRadiusSmall: '6px',
    fontSize: '14px',
    fontFamily:
      "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif",
    fontFamilyMono: "ui-monospace, 'SF Mono', Consolas, 'Courier New', monospace",
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
