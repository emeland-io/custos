<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import { leafTitle, type TreeNode } from '../api'
import Badge from './Badge.vue'

defineProps<{ node: TreeNode }>()
const open = ref(true)
</script>

<template>
  <li>
    <div class="flex items-center gap-2 py-1">
      <button
        type="button"
        class="w-4 text-stone-400 hover:text-stone-700"
        :class="{ invisible: !node.children.length && !node.leaves.length }"
        :aria-label="open ? 'Collapse' : 'Expand'"
        @click="open = !open"
      >
        {{ open ? '▾' : '▸' }}
      </button>
      <span class="text-emerald-700" aria-hidden="true">◆</span>
      <RouterLink :to="`/nodes/${node.id}`" class="font-medium hover:underline">{{ node.displayName }}</RouterLink>
      <Badge>v{{ node.version }}</Badge>
      <span v-if="node.requiredAttestation.predicateType" class="truncate font-mono text-xs text-stone-400">
        {{ node.requiredAttestation.predicateType }}
      </span>
    </div>
    <ul v-if="open" class="ml-6 border-l border-stone-200 pl-2">
      <li v-for="l in node.leaves" :key="l.id" class="flex items-center gap-2 py-1">
        <span class="w-4" />
        <span class="text-sky-600" aria-hidden="true">●</span>
        <RouterLink :to="`/leaves/${l.id}`" class="hover:underline">{{ leafTitle(l) }}</RouterLink>
        <Badge>v{{ l.version }}</Badge>
      </li>
      <TreeItem v-for="c in node.children" :key="c.id" :node="c" />
    </ul>
  </li>
</template>
