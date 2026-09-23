<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api, type LeafInput } from '../api'
import { useAction, useLoad } from '../composables'
import Badge from '../components/Badge.vue'
import Breadcrumb from '../components/Breadcrumb.vue'
import ErrorMessage from '../components/ErrorMessage.vue'
import LeafForm from '../components/LeafForm.vue'
import MarkdownView from '../components/MarkdownView.vue'
import VersionHistory from '../components/VersionHistory.vue'

const props = defineProps<{ id: string }>()
const router = useRouter()
const leaf = useLoad(() => api.leaf(props.id))
const path = useLoad(() => api.leafPath(props.id))
const history = useLoad(() => api.leafHistory(props.id))
const action = useAction()
const mode = ref<'view' | 'edit' | 'version'>('view')
const latest = computed(() => history.data.value?.at(-1))

async function edit(input: LeafInput) {
  const l = await action.run(() => api.updateLeaf(props.id, input))
  if (l) {
    leaf.data.value = l
    mode.value = 'view'
  }
}

async function newVersion(input: LeafInput) {
  const l = await action.run(() => api.newLeafVersion(props.id, input))
  if (l) void router.push(`/leaves/${l.id}`)
}

async function remove() {
  if (!confirm('Delete this task and its previous versions?')) return
  const parent = leaf.data.value?.parentId
  await action.run(() => api.deleteLeaf(props.id))
  if (!action.error.value) void router.push(`/nodes/${parent}`)
}
</script>

<template>
  <ErrorMessage :error="leaf.error.value" />
  <template v-if="leaf.data.value">
    <Breadcrumb :path="path.data.value ?? []" />
    <div
      v-if="leaf.data.value.superseded && latest"
      class="mb-4 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900"
    >
      This is an old version.
      <RouterLink :to="`/leaves/${latest.id}`" class="font-medium underline">Go to version {{ latest.version }}</RouterLink>
    </div>

    <div class="mb-4 flex flex-wrap items-center justify-between gap-2">
      <div class="flex items-center gap-2">
        <span class="text-sky-600" aria-hidden="true">●</span>
        <span class="text-sm font-semibold text-stone-500 uppercase">Task</span>
        <Badge :tone="leaf.data.value.superseded ? 'neutral' : 'good'">v{{ leaf.data.value.version }}</Badge>
      </div>
      <div v-if="!leaf.data.value.superseded && mode === 'view'" class="flex gap-2">
        <button class="btn" @click="mode = 'edit'">Edit</button>
        <button class="btn" @click="mode = 'version'">New version</button>
        <button class="btn btn-danger" @click="remove">Delete</button>
      </div>
    </div>
    <ErrorMessage :error="action.error.value" class="mb-4" />

    <div class="grid gap-6 lg:grid-cols-3">
      <section class="card lg:col-span-2">
        <template v-if="mode === 'view'">
          <MarkdownView :source="leaf.data.value.description" />
        </template>
        <template v-else>
          <p v-if="mode === 'edit'" class="mb-3 text-sm text-stone-500">
            Editing keeps the version. Create a new version instead if existing answers should become stale.
          </p>
          <LeafForm
            :initial="leaf.data.value"
            :ask-version="mode === 'version'"
            :submit-label="mode === 'edit' ? 'Save' : 'Create version'"
            :busy="action.busy.value"
            @submit="mode === 'edit' ? edit($event) : newVersion($event)"
            @cancel="mode = 'view'"
          />
        </template>
      </section>
      <section class="card">
        <h2 class="section-title">Versions</h2>
        <VersionHistory v-if="history.data.value" :versions="history.data.value" :current="id" kind="leaves" />
      </section>
    </div>
  </template>
</template>
