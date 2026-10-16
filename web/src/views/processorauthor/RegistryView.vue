<script setup lang="ts">
import { ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { errorMessage, getCatalog, listProcessors } from '../../api/authors'
import { useAuthorRole } from '../../router/authors'
import type { ProcessorInfo } from '../../types/processorAuthor'

const allowed = useAuthorRole('processor_author')
const processors = ref<ProcessorInfo[]>([])
const taskTitles = ref(new Map<string, string>())
const loading = ref(false)
const error = ref('')

async function load() {
  if (!allowed.value) return
  loading.value = true
  error.value = ''
  try {
    processors.value = await listProcessors()
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
  try {
    const c = await getCatalog()
    taskTitles.value = new Map(c.tasks.map((t) => [t.id, t.title] as const))
  } catch {
    // Titles are a convenience; ids are shown instead.
  }
}

watch(allowed, () => void load(), { immediate: true })

const imageName = (p: ProcessorInfo) => p.image.split('@')[0] ?? p.image
</script>

<template>
  <section class="mx-auto max-w-6xl space-y-6 p-6">
    <p v-if="!allowed" class="text-gray-700">The processor registry needs the processor-author role.</p>
    <template v-else>
      <header class="space-y-1">
        <h1 class="text-2xl font-semibold">Processors</h1>
        <p class="text-sm text-gray-600">
          The registry on the catalog's main. Changing an image or a binding is a catalog change made in a draft.
        </p>
      </header>
      <p v-if="loading" class="text-gray-600">Loading…</p>
      <p v-else-if="error" class="text-red-700">{{ error }}</p>
      <p v-else-if="processors.length === 0" class="text-gray-600">No processors are registered.</p>
      <table v-else class="w-full text-left text-sm">
        <thead>
          <tr class="border-b">
            <th class="py-2">Name</th>
            <th>Image</th>
            <th>Digest</th>
            <th>Timeout</th>
            <th>Network</th>
            <th>Secrets</th>
            <th>Bound tasks</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in processors" :key="p.name" class="border-b align-top">
            <td class="py-2 font-mono">{{ p.name }}</td>
            <td class="font-mono text-xs">{{ imageName(p) }}</td>
            <td class="font-mono text-xs" :title="p.digest">{{ p.digest.slice(0, 19) }}…</td>
            <td>{{ p.timeout }}</td>
            <td>{{ p.network ? 'yes' : 'no' }}</td>
            <td>{{ p.secrets.length > 0 ? p.secrets.join(', ') : '—' }}</td>
            <td>
              <ul v-if="p.bound_tasks.length > 0">
                <li v-for="id in p.bound_tasks" :key="id">{{ taskTitles.get(id) ?? id }}</li>
              </ul>
              <span v-else class="text-gray-500">none</span>
            </td>
            <td class="space-x-3 whitespace-nowrap text-right">
              <RouterLink :to="`/processors/${p.name}/runs`" class="text-blue-700 hover:underline">Runs</RouterLink>
              <RouterLink :to="`/processors/${p.name}/dry-run`" class="text-blue-700 hover:underline">Dry run</RouterLink>
            </td>
          </tr>
        </tbody>
      </table>
    </template>
  </section>
</template>
