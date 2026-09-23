<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { formatDate, type Meta, type Versioned } from '../api'
import Badge from './Badge.vue'

defineProps<{ versions: (Meta & Versioned)[]; current: string; kind: 'nodes' | 'leaves' }>()
</script>

<template>
  <ol class="space-y-1 text-sm">
    <li v-for="v in [...versions].reverse()" :key="v.id" class="flex items-center gap-2">
      <Badge :tone="v.superseded ? 'neutral' : 'good'">v{{ v.version }}</Badge>
      <span v-if="v.id === current" class="font-medium">this version</span>
      <RouterLink v-else :to="`/${kind}/${v.id}`" class="text-emerald-800 hover:underline">view</RouterLink>
      <span class="text-stone-400">{{ formatDate(v.createdAt) }}</span>
    </li>
  </ol>
</template>
