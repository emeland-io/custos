<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api, leafTitle, type NodeInput } from '../api'
import { useAction, useLoad } from '../composables'
import Badge from '../components/Badge.vue'
import Breadcrumb from '../components/Breadcrumb.vue'
import ErrorMessage from '../components/ErrorMessage.vue'
import LeafForm from '../components/LeafForm.vue'
import MarkdownView from '../components/MarkdownView.vue'
import NodeForm from '../components/NodeForm.vue'
import VersionHistory from '../components/VersionHistory.vue'

const props = defineProps<{ id: string }>()
const router = useRouter()
const node = useLoad(() => api.node(props.id))
const path = useLoad(() => api.nodePath(props.id))
const history = useLoad(() => api.nodeHistory(props.id))
const subtree = useLoad(() => api.subtree(props.id))
const action = useAction()

type Mode = 'view' | 'edit' | 'version' | 'child' | 'leaf'
const mode = ref<Mode>('view')

const latest = computed(() => history.data.value?.at(-1))
const parents = computed(() => (path.data.value ?? []).slice(0, -1))

async function edit(input: NodeInput) {
  const n = await action.run(() => api.updateNode(props.id, input))
  if (n) {
    node.data.value = n
    mode.value = 'view'
  }
}

async function newVersion(input: NodeInput) {
  const n = await action.run(() => api.newNodeVersion(props.id, input))
  if (n) void router.push(`/nodes/${n.id}`)
}

async function addChild(input: NodeInput) {
  const n = await action.run(() => api.createNode({ ...input, parentId: props.id }))
  if (n) void router.push(`/nodes/${n.id}`)
}

async function addLeaf(input: { parentId: string; description: string; version?: string }) {
  const l = await action.run(() => api.createLeaf({ ...input, parentId: props.id }))
  if (l) void router.push(`/leaves/${l.id}`)
}

async function remove() {
  if (!confirm('Delete this node and its previous versions?')) return
  const parent = node.data.value?.parentId
  await action.run(() => api.deleteNode(props.id))
  if (!action.error.value) void router.push(parent ? `/nodes/${parent}` : '/roots')
}
</script>

<template>
  <ErrorMessage :error="node.error.value" />
  <template v-if="node.data.value">
    <Breadcrumb :path="parents" />
    <div
      v-if="node.data.value.superseded && latest"
      class="mb-4 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900"
    >
      This is an old version.
      <RouterLink :to="`/nodes/${latest.id}`" class="font-medium underline">Go to version {{ latest.version }}</RouterLink>
    </div>

    <div class="mb-6 flex flex-wrap items-start justify-between gap-2">
      <div class="flex items-center gap-2">
        <span class="text-emerald-700" aria-hidden="true">◆</span>
        <h1 class="text-2xl font-bold tracking-tight">{{ node.data.value.displayName }}</h1>
        <Badge :tone="node.data.value.superseded ? 'neutral' : 'good'">v{{ node.data.value.version }}</Badge>
      </div>
      <div v-if="!node.data.value.superseded && mode === 'view'" class="flex flex-wrap gap-2">
        <button class="btn" @click="mode = 'child'">Add node</button>
        <button class="btn" @click="mode = 'leaf'">Add task</button>
        <button class="btn" @click="mode = 'edit'">Edit</button>
        <button class="btn" @click="mode = 'version'">New version</button>
        <button class="btn btn-danger" @click="remove">Delete</button>
      </div>
    </div>
    <ErrorMessage :error="action.error.value" class="mb-4" />

    <section v-if="mode !== 'view'" class="card mb-6">
      <h2 class="section-title">
        {{ { edit: 'Edit node', version: 'New version', child: 'New child node', leaf: 'New task', view: '' }[mode] }}
      </h2>
      <p v-if="mode === 'edit'" class="mb-3 text-sm text-stone-500">
        Editing keeps the version. Create a new version instead if existing attestations should no longer count.
      </p>
      <NodeForm
        v-if="mode === 'edit'"
        :initial="node.data.value"
        submit-label="Save"
        :busy="action.busy.value"
        @submit="edit"
        @cancel="mode = 'view'"
      />
      <NodeForm
        v-if="mode === 'version'"
        :initial="node.data.value"
        ask-version
        submit-label="Create version"
        :busy="action.busy.value"
        @submit="newVersion"
        @cancel="mode = 'view'"
      />
      <NodeForm
        v-if="mode === 'child'"
        :parent-id="id"
        submit-label="Create node"
        :busy="action.busy.value"
        @submit="addChild"
        @cancel="mode = 'view'"
      />
      <LeafForm
        v-if="mode === 'leaf'"
        :parent-id="id"
        submit-label="Create task"
        :busy="action.busy.value"
        @submit="addLeaf"
        @cancel="mode = 'view'"
      />
    </section>

    <div class="grid gap-6 lg:grid-cols-3">
      <div class="space-y-6 lg:col-span-2">
        <section class="card">
          <h2 class="section-title">Description</h2>
          <MarkdownView :source="node.data.value.description" />
        </section>

        <section class="card">
          <h2 class="section-title">Tasks and child nodes</h2>
          <ul v-if="subtree.data.value" class="space-y-1">
            <li v-for="l in subtree.data.value.leaves" :key="l.id" class="flex items-center gap-2">
              <span class="text-sky-600" aria-hidden="true">●</span>
              <RouterLink :to="`/leaves/${l.id}`" class="hover:underline">{{ leafTitle(l) }}</RouterLink>
              <Badge>v{{ l.version }}</Badge>
            </li>
            <li v-for="c in subtree.data.value.children" :key="c.id" class="flex items-center gap-2">
              <span class="text-emerald-700" aria-hidden="true">◆</span>
              <RouterLink :to="`/nodes/${c.id}`" class="font-medium hover:underline">{{ c.displayName }}</RouterLink>
              <Badge>v{{ c.version }}</Badge>
            </li>
          </ul>
          <p
            v-if="subtree.data.value && !subtree.data.value.leaves.length && !subtree.data.value.children.length"
            class="text-sm text-stone-500"
          >
            Nothing below this node yet.
          </p>
        </section>
      </div>

      <div class="space-y-6">
        <section class="card">
          <h2 class="section-title">Required attestation</h2>
          <dl class="space-y-2 text-sm">
            <div>
              <dt class="label">Predicate type</dt>
              <dd class="font-mono break-all">{{ node.data.value.requiredAttestation.predicateType || '—' }}</dd>
            </div>
            <div v-if="node.data.value.requiredAttestation.subjectName">
              <dt class="label">Subject name</dt>
              <dd class="font-mono break-all">{{ node.data.value.requiredAttestation.subjectName }}</dd>
            </div>
            <div v-if="node.data.value.requiredAttestation.requiredIdentities?.length">
              <dt class="label">Signed by</dt>
              <dd v-for="i in node.data.value.requiredAttestation.requiredIdentities" :key="i" class="font-mono text-xs break-all">
                {{ i }}
              </dd>
            </div>
          </dl>
        </section>

        <section class="card">
          <h2 class="section-title">Why it is missing</h2>
          <MarkdownView :source="node.data.value.missingReason" empty="No reason given." />
        </section>

        <section class="card">
          <h2 class="section-title">Versions</h2>
          <VersionHistory v-if="history.data.value" :versions="history.data.value" :current="id" kind="nodes" />
        </section>
      </div>
    </div>
  </template>
</template>
