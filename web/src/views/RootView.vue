<script setup lang="ts">
import { reactive, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '../api'
import { useAction, useLoad } from '../composables'
import MarkdownView from '../components/MarkdownView.vue'
import MarkdownField from '../components/MarkdownField.vue'
import ErrorMessage from '../components/ErrorMessage.vue'
import TreeItem from '../components/TreeItem.vue'

const props = defineProps<{ id: string }>()
const router = useRouter()
const root = useLoad(() => api.root(props.id))
const tree = useLoad(() => api.tree(props.id))
const seeds = useLoad(async () => (await api.seeds()).filter((s) => s.rootId === props.id))
const editing = ref(false)
const form = reactive({ displayName: '', description: '' })
const action = useAction()

function edit() {
  if (!root.data.value) return
  form.displayName = root.data.value.displayName
  form.description = root.data.value.description
  editing.value = true
}

async function save() {
  const r = root.data.value
  if (!r) return
  const updated = await action.run(() => api.updateRoot(r.id, { ...form, entryNodeId: r.entryNodeId }))
  if (updated) {
    root.data.value = updated
    editing.value = false
  }
}

async function remove() {
  if (!confirm('Delete this root? Its nodes and leaves are kept.')) return
  await action.run(() => api.deleteRoot(props.id))
  if (!action.error.value) void router.push('/roots')
}

async function startSeed() {
  const r = root.data.value
  if (!r) return
  const name = prompt('Name of the new seed', `${r.displayName} – ${new Date().toLocaleDateString()}`)
  if (!name) return
  const seed = await action.run(() => api.createSeed({ displayName: name, description: '', rootId: r.id }))
  if (seed) void router.push(`/seeds/${seed.id}`)
}
</script>

<template>
  <ErrorMessage :error="root.error.value" />
  <template v-if="root.data.value">
    <div v-if="!editing" class="mb-6">
      <div class="mb-2 flex flex-wrap items-start justify-between gap-2">
        <h1 class="text-2xl font-bold tracking-tight">{{ root.data.value.displayName }}</h1>
        <div class="flex gap-2">
          <button class="btn btn-primary" @click="startSeed">Start seed</button>
          <button class="btn" @click="edit">Edit</button>
          <button class="btn btn-danger" @click="remove">Delete</button>
        </div>
      </div>
      <MarkdownView :source="root.data.value.description" />
    </div>
    <form v-else class="card mb-6 space-y-4" @submit.prevent="save">
      <label class="block">
        <span class="label">Display name</span>
        <input v-model="form.displayName" class="input" required />
      </label>
      <MarkdownField v-model="form.description" label="Description" />
      <div class="flex gap-2">
        <button class="btn btn-primary" :disabled="action.busy.value">Save</button>
        <button type="button" class="btn" @click="editing = false">Cancel</button>
      </div>
    </form>
    <ErrorMessage :error="action.error.value" />

    <section class="card mb-6">
      <h2 class="section-title">Tree</h2>
      <ErrorMessage :error="tree.error.value" />
      <ul v-if="tree.data.value" class="-ml-1"><TreeItem :node="tree.data.value" /></ul>
      <p class="mt-3 text-xs text-stone-500">Open a node to add child nodes and tasks.</p>
    </section>

    <section class="card">
      <h2 class="section-title">Seeds of this root</h2>
      <p v-if="!seeds.data.value?.length" class="text-sm text-stone-500">No seeds yet.</p>
      <ul class="space-y-1">
        <li v-for="s in seeds.data.value" :key="s.id">
          <RouterLink :to="`/seeds/${s.id}`" class="text-emerald-800 hover:underline">{{ s.displayName }}</RouterLink>
        </li>
      </ul>
    </section>
  </template>
</template>
