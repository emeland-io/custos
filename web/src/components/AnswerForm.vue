<script setup lang="ts">
import { ref, watch } from 'vue'
import MarkdownField from './MarkdownField.vue'

defineProps<{ busy?: boolean; submitLabel?: string }>()
const emit = defineEmits<{ submit: [{ content: string; author: string }]; cancel: [] }>()

const authorKey = 'custos.author'
function storedAuthor(): string {
  try {
    return localStorage.getItem(authorKey) ?? ''
  } catch {
    return ''
  }
}

const content = ref('')
const author = ref(storedAuthor())
watch(author, (a) => {
  try {
    localStorage.setItem(authorKey, a)
  } catch {
    // Remembering the author is a convenience only.
  }
})
</script>

<template>
  <form class="space-y-3" @submit.prevent="emit('submit', { content, author })">
    <MarkdownField v-model="content" label="Answer" :rows="5" placeholder="Result or reply, in Markdown." />
    <div class="flex flex-wrap items-end gap-2">
      <label class="block">
        <span class="label">Author</span>
        <input v-model="author" class="input" />
      </label>
      <button class="btn btn-primary" :disabled="busy || !content.trim()">{{ submitLabel ?? 'Save answer' }}</button>
      <button type="button" class="btn" @click="emit('cancel')">Cancel</button>
    </div>
  </form>
</template>
