<script setup lang="ts">
import AppIcon, { type IconName } from './AppIcon.vue'

withDefaults(
  defineProps<{
    label: string
    value: string
    sub?: string
    icon: IconName
    accent?: 'blue' | 'cyan' | 'violet' | 'green' | 'amber'
  }>(),
  { sub: '', accent: 'blue' },
)
</script>

<template>
  <div class="stat-card">
    <div class="stat-icon" :class="`accent-${accent}`">
      <AppIcon :name="icon" :size="18" />
    </div>
    <div class="stat-body">
      <div class="stat-label">{{ label }}</div>
      <div class="stat-value">{{ value }}</div>
      <div v-if="sub" class="stat-sub">{{ sub }}</div>
    </div>
  </div>
</template>

<style scoped>
.stat-card {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 18px 20px;
  background: var(--surface);
  border: 1px solid var(--border-soft);
  border-radius: 14px;
  transition:
    border-color 0.2s ease,
    transform 0.2s ease,
    box-shadow 0.2s ease;
}
.stat-card:hover {
  border-color: rgba(91, 140, 255, 0.35);
  box-shadow: 0 8px 24px rgba(2, 6, 18, 0.5);
  transform: translateY(-1px);
}
.stat-icon {
  width: 42px;
  height: 42px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex: none;
}
.stat-icon.accent-blue {
  color: #7ba3ff;
  background: rgba(91, 140, 255, 0.12);
  box-shadow: inset 0 0 0 1px rgba(91, 140, 255, 0.25);
}
.stat-icon.accent-cyan {
  color: #4ed8f0;
  background: rgba(34, 211, 238, 0.1);
  box-shadow: inset 0 0 0 1px rgba(34, 211, 238, 0.22);
}
.stat-icon.accent-violet {
  color: #b39bff;
  background: rgba(139, 92, 246, 0.12);
  box-shadow: inset 0 0 0 1px rgba(139, 92, 246, 0.25);
}
.stat-icon.accent-green {
  color: #4ade9d;
  background: rgba(52, 211, 153, 0.1);
  box-shadow: inset 0 0 0 1px rgba(52, 211, 153, 0.22);
}
.stat-icon.accent-amber {
  color: #fbbf24;
  background: rgba(251, 191, 36, 0.1);
  box-shadow: inset 0 0 0 1px rgba(251, 191, 36, 0.22);
}
.stat-body {
  min-width: 0;
}
.stat-label {
  font-size: 12px;
  color: var(--text-3);
  letter-spacing: 0.02em;
}
.stat-value {
  margin-top: 2px;
  font-size: 24px;
  font-weight: 700;
  line-height: 1.2;
  color: var(--text);
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.01em;
}
.stat-sub {
  margin-top: 2px;
  font-size: 12px;
  color: var(--text-4);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
