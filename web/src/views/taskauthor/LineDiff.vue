<script setup lang="ts">
import { computed } from 'vue'
import { lineDiff } from './lineDiff'

const props = defineProps<{ before: string; after: string }>()

const lines = computed(() => lineDiff(props.before, props.after))
const changed = computed(() => lines.value.some((l) => l.op !== 'same'))
</script>

<template>
  <div class="overflow-x-auto rounded border border-gray-200 text-sm">
    <p v-if="!changed" class="px-3 py-2 text-gray-500">No differences.</p>
    <div v-else class="py-1 font-mono leading-5">
      <div
        v-for="(l, i) in lines"
        :key="i"
        class="whitespace-pre-wrap px-3"
        :class="l.op === 'add' ? 'bg-green-50 text-green-900' : l.op === 'del' ? 'bg-red-50 text-red-900' : 'text-gray-700'"
      >{{ l.op === 'add' ? '+ ' : l.op === 'del' ? '- ' : '  ' }}{{ l.text }}</div>
    </div>
  </div>
</template>
