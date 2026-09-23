<script setup lang="ts">
import { computed } from 'vue'
import MarkdownIt from 'markdown-it'
import DOMPurify from 'dompurify'

const props = defineProps<{ source?: string; empty?: string }>()

const md = new MarkdownIt({ html: false, linkify: true, typographer: true })

const html = computed(() => DOMPurify.sanitize(md.render(props.source ?? '')))
</script>

<template>
  <div v-if="source?.trim()" class="prose prose-stone prose-sm max-w-none" v-html="html" />
  <p v-else class="text-sm text-stone-400 italic">{{ empty ?? 'No description.' }}</p>
</template>
