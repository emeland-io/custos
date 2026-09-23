<script setup lang="ts">
import { reactive } from 'vue'
import type { Leaf, LeafInput } from '../api'
import MarkdownField from './MarkdownField.vue'

const props = defineProps<{
  initial?: Leaf
  parentId?: string
  askVersion?: boolean
  submitLabel: string
  busy?: boolean
}>()
const emit = defineEmits<{ submit: [LeafInput]; cancel: [] }>()

const form = reactive({ description: props.initial?.description ?? '', version: '' })

function submit() {
  emit('submit', {
    parentId: props.initial?.parentId ?? props.parentId ?? '',
    description: form.description,
    version: form.version.trim() || undefined,
  })
}
</script>

<template>
  <form class="space-y-4" @submit.prevent="submit">
    <label v-if="askVersion" class="block max-w-xs">
      <span class="label">New version</span>
      <input v-model="form.version" class="input" required :placeholder="initial ? `after ${initial.version}` : '1'" />
    </label>
    <MarkdownField
      v-model="form.description"
      label="Task"
      :rows="8"
      placeholder="# Short title&#10;&#10;What needs to be done, in Markdown."
    />
    <div class="flex gap-2">
      <button class="btn btn-primary" :disabled="busy">{{ submitLabel }}</button>
      <button type="button" class="btn" @click="emit('cancel')">Cancel</button>
    </div>
  </form>
</template>
