import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * 全局主题：冷色调（蓝 / 白 / 灰），简洁风。
 * 覆盖 Naive UI 默认的绿色主色。
 */
export const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#2563eb',
    primaryColorHover: '#3b82f6',
    primaryColorPressed: '#1d4ed8',
    primaryColorSuppl: '#3b82f6',
    infoColor: '#0ea5e9',
    infoColorHover: '#38bdf8',
    infoColorPressed: '#0284c7',
    infoColorSuppl: '#38bdf8',

    bodyColor: '#f5f7fa',
    cardColor: '#ffffff',
    modalColor: '#ffffff',
    popoverColor: '#ffffff',
    tableColor: '#ffffff',
    inputColor: '#ffffff',
    actionColor: '#f8fafc',
    hoverColor: '#f1f5f9',

    textColorBase: '#0f172a',
    textColor1: '#0f172a',
    textColor2: '#334155',
    textColor3: '#64748b',
    placeholderColor: '#94a3b8',
    borderColor: '#e2e8f0',
    dividerColor: '#eef2f7',

    borderRadius: '8px',
    borderRadiusSmall: '6px',
    fontSize: '14px',
    fontFamily:
      "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif",
    fontFamilyMono: "ui-monospace, 'SF Mono', Consolas, 'Courier New', monospace",
  },
  Layout: {
    color: '#f5f7fa',
    siderColor: '#ffffff',
  },
  Menu: {
    borderRadius: '8px',
    itemHeight: '40px',
    itemTextColor: '#475569',
    itemTextColorHover: '#0f172a',
    itemTextColorActive: '#2563eb',
    itemTextColorChildActive: '#2563eb',
    itemColorHover: 'rgba(15, 23, 42, 0.04)',
    itemColorActive: 'rgba(37, 99, 235, 0.08)',
    itemColorActiveHover: 'rgba(37, 99, 235, 0.12)',
  },
  Card: {
    borderColor: '#e8edf3',
    borderRadius: '12px',
    titleFontWeight: '600',
    titleFontSizeMedium: '15px',
    paddingMedium: '16px 20px',
  },
  DataTable: {
    borderColor: '#eef2f7',
    thColor: '#f8fafc',
    thColorHover: '#f1f5f9',
    tdColorHover: '#f8fafc',
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
    borderRadius: '12px',
  },
}
