<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../../api/client'
import type { Workspace } from '../../types/workspace'

const route = useRoute()
const workspaceId = computed(() => route.params.id as string)

const frozen = ref(false)
const warning = ref('')
const errorMessage = ref('')
const toggling = ref(false)

async function load() {
  try {
    const list = await api.get<Workspace[]>('/api/workspaces')
    const w = list.find((x) => x.id === workspaceId.value)
    frozen.value = w?.frozen ?? false
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  }
}

onMounted(load)

async function toggleFreeze() {
  toggling.value = true
  warning.value = ''
  errorMessage.value = ''
  try {
    const path = frozen.value ? 'unfreeze' : 'freeze'
    const res = await api.post<{ frozen: boolean; warning?: string }>(`/api/workspaces/${workspaceId.value}/${path}`)
    frozen.value = res.frozen
    if (res.warning) warning.value = res.warning
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  } finally {
    toggling.value = false
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-xl font-semibold">Book</h1>
      <div class="flex items-center gap-3">
        <a :href="`/api/workspaces/${workspaceId}/book?format=markdown`" class="text-sm text-gray-600 hover:text-gray-900" download>
          Download Markdown
        </a>
        <button
          class="rounded border px-3 py-1.5 text-sm font-medium disabled:opacity-50"
          :disabled="toggling"
          @click="toggleFreeze"
        >
          {{ frozen ? 'Unfreeze' : 'Freeze' }}
        </button>
      </div>
    </div>
    <p v-if="warning" class="text-sm text-amber-700">{{ warning }}</p>
    <p v-if="errorMessage" class="text-sm text-red-600">{{ errorMessage }}</p>
    <iframe :src="`/api/workspaces/${workspaceId}/book?format=html`" class="h-[80vh] w-full rounded border bg-white" title="Workspace book" />
  </div>
</template>
