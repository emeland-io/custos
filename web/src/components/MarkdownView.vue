<script setup lang="ts">
import { computed } from 'vue'
import { renderMarkdown } from '../lib/markdown'

const props = defineProps<{ source: string }>()
const html = computed(() => renderMarkdown(props.source))
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -->
  <div class="markdown-view" v-html="html" />
</template>

<style scoped>
.markdown-view :deep(h1),
.markdown-view :deep(h2),
.markdown-view :deep(h3) {
  font-weight: 600;
  margin: 0.75em 0 0.25em;
}
.markdown-view :deep(p) {
  margin: 0.5em 0;
}
.markdown-view :deep(ul),
.markdown-view :deep(ol) {
  margin: 0.5em 0 0.5em 1.5em;
}
.markdown-view :deep(code) {
  background: #f3f4f6;
  padding: 0 0.25em;
  border-radius: 0.25em;
  font-size: 0.9em;
}
.markdown-view :deep(pre) {
  background: #f3f4f6;
  padding: 0.75em;
  border-radius: 0.375em;
  overflow-x: auto;
}
</style>
