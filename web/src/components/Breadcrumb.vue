<script setup lang="ts">
import { RouterLink } from 'vue-router'

defineProps<{ path: { id: string; displayName: string }[] }>()
</script>

<template>
  <nav v-if="path.length" class="mb-2 flex flex-wrap items-center gap-1 text-sm text-stone-500">
    <template v-for="(p, i) in path" :key="p.id">
      <span v-if="i > 0" class="text-stone-300">/</span>
      <RouterLink :to="`/nodes/${p.id}`" class="hover:text-emerald-800 hover:underline">{{ p.displayName }}</RouterLink>
    </template>
  </nav>
</template>
