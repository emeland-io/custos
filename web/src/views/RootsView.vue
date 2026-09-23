<script setup lang="ts">
import { reactive, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '../api'
import { useAction, useLoad } from '../composables'
import MarkdownView from '../components/MarkdownView.vue'
import MarkdownField from '../components/MarkdownField.vue'
import ErrorMessage from '../components/ErrorMessage.vue'

const router = useRouter()
const { data: roots, error } = useLoad(api.roots)
const creating = ref(false)
const form = reactive({ displayName: '', description: '', entryName: '' })
const action = useAction()

// create makes the entry Node first, then the Root starting at it.
async function create() {
  const root = await action.run(async () => {
    const entry = await api.createNode({
      displayName: form.entryName.trim() || form.displayName.trim(),
      description: '',
      requiredAttestation: { predicateType: '' },
      missingReason: '',
    })
    return api.createRoot({ displayName: form.displayName.trim(), description: form.description, entryNodeId: entry.id })
  })
  if (root) void router.push(`/roots/${root.id}`)
}
</script>

<template>
  <div class="mb-6 flex items-center justify-between">
    <h1 class="text-2xl font-bold tracking-tight">Roots</h1>
    <button v-if="!creating" class="btn btn-primary" @click="creating = true">New root</button>
  </div>
  <ErrorMessage :error="error" />

  <form v-if="creating" class="card mb-6 space-y-4" @submit.prevent="create">
    <div class="grid gap-4 sm:grid-cols-2">
      <label class="block">
        <span class="label">Display name</span>
        <input v-model="form.displayName" class="input" required />
      </label>
      <label class="block">
        <span class="label">Entry node name</span>
        <input v-model="form.entryName" class="input" placeholder="Same as the root" />
      </label>
    </div>
    <MarkdownField v-model="form.description" label="Description" :rows="4" />
    <ErrorMessage :error="action.error.value" />
    <div class="flex gap-2">
      <button class="btn btn-primary" :disabled="action.busy.value">Create</button>
      <button type="button" class="btn" @click="creating = false">Cancel</button>
    </div>
  </form>

  <p v-if="roots && !roots.length && !creating" class="text-stone-500">
    No roots yet. A root is the entry point to a tree of nodes and tasks.
  </p>
  <ul class="grid gap-3">
    <li v-for="r in roots" :key="r.id">
      <RouterLink :to="`/roots/${r.id}`" class="card block hover:border-emerald-600">
        <h2 class="mb-1 font-semibold">{{ r.displayName }}</h2>
        <div class="line-clamp-3"><MarkdownView :source="r.description" /></div>
      </RouterLink>
    </li>
  </ul>
</template>
