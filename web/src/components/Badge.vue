<script setup lang="ts">
defineProps<{ tone?: 'neutral' | 'good' | 'warn' | 'bad' | 'info' }>()
</script>

<template>
  <span
    class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap"
    :class="{
      'bg-stone-100 text-stone-700': !tone || tone === 'neutral',
      'bg-emerald-100 text-emerald-800': tone === 'good',
      'bg-amber-100 text-amber-800': tone === 'warn',
      'bg-rose-100 text-rose-800': tone === 'bad',
      'bg-sky-100 text-sky-800': tone === 'info',
    }"
  >
    <slot />
  </span>
</template>
