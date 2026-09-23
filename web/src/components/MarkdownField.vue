<script setup lang="ts">
import { ref } from 'vue'
import MarkdownView from './MarkdownView.vue'

defineProps<{ label: string; rows?: number; placeholder?: string }>()
const model = defineModel<string>({ default: '' })
const preview = ref(false)
</script>

<template>
  <div>
    <div class="mb-1 flex items-center justify-between">
      <span class="label !mb-0">{{ label }}</span>
      <div class="flex gap-1 text-xs">
        <button
          type="button"
          class="rounded px-2 py-0.5"
          :class="!preview ? 'bg-stone-200 font-semibold' : 'text-stone-500'"
          @click="preview = false"
        >
          Write
        </button>
        <button
          type="button"
          class="rounded px-2 py-0.5"
          :class="preview ? 'bg-stone-200 font-semibold' : 'text-stone-500'"
          @click="preview = true"
        >
          Preview
        </button>
      </div>
    </div>
    <textarea
      v-if="!preview"
      v-model="model"
      class="input font-mono"
      :rows="rows ?? 6"
      :placeholder="placeholder ?? 'Markdown'"
    />
    <div v-else class="min-h-24 rounded-md border border-stone-200 bg-stone-50 p-3">
      <MarkdownView :source="model" empty="Nothing to preview." />
    </div>
  </div>
</template>
