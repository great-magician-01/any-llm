<script setup lang="ts">
withDefaults(defineProps<{ size?: number }>(), { size: 32 })
</script>

<template>
  <svg
    class="brand-mark"
    :width="size"
    :height="size"
    viewBox="0 0 48 48"
    xmlns="http://www.w3.org/2000/svg"
    aria-hidden="true"
  >
    <defs>
      <linearGradient id="brand-bg" x1="0" y1="0" x2="48" y2="48" gradientUnits="userSpaceOnUse">
        <!-- 渐变端点走品牌变量：跟随配色主题（data-accent）换肤 -->
        <stop offset="0" style="stop-color: var(--brand)" />
        <stop offset="1" style="stop-color: var(--brand-2)" />
      </linearGradient>
    </defs>
    <rect width="48" height="48" rx="11" fill="url(#brand-bg)" />
    <g
      fill="none"
      stroke="#fff"
      stroke-width="3"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path d="M14 13.5C18 13.5 19.8 20.6 24.4 22.6" />
      <path d="M14 24h9" />
      <path d="M14 34.5C18 34.5 19.8 27.4 24.4 25.4" />
      <path d="M25 24h9.2" />
      <path d="M30.6 19.2 36.5 24l-5.9 4.8" />
    </g>
    <circle cx="11.3" cy="13.5" r="2.3" fill="#fff" />
    <circle cx="11.3" cy="24" r="2.3" fill="#fff" />
    <circle cx="11.3" cy="34.5" r="2.3" fill="#fff" />
  </svg>
</template>

<style scoped>
.brand-mark {
  display: block;
  flex: none;
  border-radius: 23%;
  box-shadow:
    0 2px 10px color-mix(in srgb, var(--brand) 45%, transparent),
    0 0 18px color-mix(in srgb, var(--brand-2) 25%, transparent);
}
</style>
