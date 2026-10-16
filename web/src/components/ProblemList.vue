<script setup lang="ts">
import type { Problem } from '../types/error'

defineProps<{ problems: Problem[] }>()
</script>

<template>
  <ul v-if="problems.length" class="list-disc space-y-1 pl-5 text-sm text-red-700">
    <li v-for="(p, i) in problems" :key="i">
      <span class="font-mono text-xs text-red-500">{{ p.path }}</span>
      <span class="text-red-400">({{ p.rule }})</span>: {{ p.message }}
    </li>
  </ul>
</template>
