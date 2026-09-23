<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api, formatDate, leafTitle } from '../api'
import { useAction, useLoad } from '../composables'
import AnswerForm from '../components/AnswerForm.vue'
import AttestationCard from '../components/AttestationCard.vue'
import Badge from '../components/Badge.vue'
import Breadcrumb from '../components/Breadcrumb.vue'
import ErrorMessage from '../components/ErrorMessage.vue'
import MarkdownView from '../components/MarkdownView.vue'
import VerificationBadge from '../components/VerificationBadge.vue'

const props = defineProps<{ id: string }>()
const router = useRouter()
const status = useLoad(() => api.seedStatus(props.id))
const shoots = useLoad(() => api.seedShoots(props.id))
const attestations = useLoad(() => api.seedAttestations(props.id))
// All versions, to name the elements that answers and attestations
// refer to.
const leaves = useLoad(() => api.leaves(true))
const nodes = useLoad(() => api.nodes(true))
const root = useLoad(async () => (await api.seed(props.id)).rootId).data
const action = useAction()

const leafById = computed(() => new Map((leaves.data.value ?? []).map((l) => [l.id, l])))
const nodeById = computed(() => new Map((nodes.data.value ?? []).map((n) => [n.id, n])))

const answering = ref<string>()
const uploadFor = ref<string>()
const fileInput = ref<HTMLInputElement>()

function reload() {
  void status.reload()
  void shoots.reload()
  void attestations.reload()
}

async function answer(leafId: string, a: { content: string; author: string }) {
  if (await action.run(() => api.createShoot({ seedId: props.id, leafId, ...a }))) {
    answering.value = undefined
    reload()
  }
}

function pickFile(nodeId: string) {
  uploadFor.value = nodeId
  fileInput.value?.click()
}

async function upload(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  const nodeId = uploadFor.value
  input.value = ''
  if (!file || !nodeId) return
  const text = await file.text()
  if (await action.run(() => api.uploadAttestation(props.id, nodeId, text))) reload()
}

async function deleteShoot(id: string) {
  if (!confirm('Delete this answer?')) return
  await action.run(() => api.deleteShoot(id))
  if (!action.error.value) reload()
}

async function remove() {
  if (!confirm('Delete this seed with all its answers and attestations?')) return
  await action.run(() => api.deleteSeed(props.id))
  if (!action.error.value) void router.push('/seeds')
}
</script>

<template>
  <ErrorMessage :error="status.error.value" />
  <input ref="fileInput" type="file" accept=".json,.jsonl,.intoto,application/json" class="hidden" @change="upload" />

  <template v-if="status.data.value">
    <div class="mb-4 flex flex-wrap items-start justify-between gap-2">
      <div>
        <h1 class="text-2xl font-bold tracking-tight">{{ status.data.value.seed.displayName }}</h1>
        <RouterLink v-if="root" :to="`/roots/${root}`" class="text-sm text-emerald-800 hover:underline">
          View the root
        </RouterLink>
      </div>
      <button class="btn btn-danger" @click="remove">Delete seed</button>
    </div>
    <MarkdownView v-if="status.data.value.seed.description" :source="status.data.value.seed.description" class="mb-4" />
    <ErrorMessage :error="action.error.value" class="mb-4" />

    <section class="card mb-6">
      <div class="mb-2 flex items-baseline justify-between">
        <h2 class="section-title !mb-0">Progress</h2>
        <span class="text-2xl font-bold text-emerald-800">{{ status.data.value.percent }}%</span>
      </div>
      <div class="mb-3 h-2 overflow-hidden rounded-full bg-stone-200" role="progressbar" :aria-valuenow="status.data.value.percent" aria-valuemin="0" aria-valuemax="100">
        <div class="h-full bg-emerald-600 transition-all" :style="{ width: `${status.data.value.percent}%` }" />
      </div>
      <p class="text-sm text-stone-600">
        {{ status.data.value.answeredLeaves }} of {{ status.data.value.leaves }} tasks answered ·
        {{ status.data.value.attestedNodes }} of {{ status.data.value.nodes }} nodes attested
      </p>
      <ErrorMessage :error="status.data.value.treeError" class="mt-3" />
      <div
        v-if="status.data.value.orphaned.length"
        class="mt-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900"
      >
        {{ status.data.value.orphaned.length }} reference(s) point to elements that no longer exist in the root directory:
        <ul class="mt-1 list-inside list-disc font-mono text-xs">
          <li v-for="o in status.data.value.orphaned" :key="o.id + o.field">{{ o.kind }} {{ o.id }} → {{ o.field }} {{ o.target }}</li>
        </ul>
      </div>
    </section>

    <section class="mb-6">
      <h2 class="section-title">Missing attestations ({{ status.data.value.missingAttestations.length }})</h2>
      <p v-if="!status.data.value.missingAttestations.length" class="text-sm text-stone-500">Every node is attested.</p>
      <ul class="space-y-3">
        <li v-for="m in status.data.value.missingAttestations" :key="m.node.id" class="card">
          <Breadcrumb :path="m.path.slice(0, -1)" />
          <div class="mb-2 flex flex-wrap items-center gap-2">
            <span class="text-emerald-700" aria-hidden="true">◆</span>
            <RouterLink :to="`/nodes/${m.node.id}`" class="font-semibold hover:underline">{{ m.node.displayName }}</RouterLink>
            <Badge>v{{ m.node.version }}</Badge>
            <span class="font-mono text-xs text-stone-500">{{ m.node.requiredAttestation.predicateType || 'any predicate' }}</span>
            <button class="btn ml-auto" :disabled="action.busy.value" @click="pickFile(m.node.id)">Upload attestation</button>
          </div>
          <MarkdownView :source="m.node.missingReason" empty="No reason given why it is missing." />
          <div v-if="m.stale.length || m.rejected.length" class="mt-3 flex flex-wrap gap-2 text-xs">
            <span v-for="a in m.stale" :key="a.id" class="inline-flex items-center gap-1">
              <Badge tone="warn">attested v{{ nodeById.get(a.nodeId)?.version ?? '?' }}, now stale</Badge>
            </span>
            <span v-for="a in m.rejected" :key="a.id" class="inline-flex items-center gap-1">
              <VerificationBadge :status="a.status" :met="a.requirementMet" />
              <span class="text-stone-400">{{ formatDate(a.createdAt) }}</span>
            </span>
          </div>
        </li>
      </ul>
    </section>

    <section class="mb-6">
      <h2 class="section-title">Open tasks ({{ status.data.value.missingShoots.length }})</h2>
      <p v-if="!status.data.value.missingShoots.length" class="text-sm text-stone-500">Every task is answered.</p>
      <ul class="space-y-3">
        <li v-for="m in status.data.value.missingShoots" :key="m.leaf.id" class="card">
          <Breadcrumb :path="m.path" />
          <div class="mb-2 flex items-center gap-2">
            <Badge>v{{ m.leaf.version }}</Badge>
            <Badge v-if="m.stale.length" tone="warn">answered for an older version</Badge>
            <RouterLink :to="`/leaves/${m.leaf.id}`" class="ml-auto text-xs text-emerald-800 hover:underline">Open task</RouterLink>
          </div>
          <MarkdownView :source="m.leaf.description" />
          <details v-for="s in m.stale" :key="s.id" class="mt-3 rounded-md bg-amber-50 p-2 text-sm">
            <summary class="cursor-pointer text-amber-900">
              Answer to v{{ leafById.get(s.leafId)?.version ?? '?' }} by {{ s.author || 'unknown' }}
            </summary>
            <MarkdownView :source="s.content" class="mt-2" />
          </details>
          <div class="mt-3">
            <AnswerForm
              v-if="answering === m.leaf.id"
              :busy="action.busy.value"
              @submit="answer(m.leaf.id, $event)"
              @cancel="answering = undefined"
            />
            <button v-else class="btn btn-primary" @click="answering = m.leaf.id">Answer</button>
          </div>
        </li>
      </ul>
    </section>

    <section class="mb-6">
      <h2 class="section-title">Answers ({{ shoots.data.value?.length ?? 0 }})</h2>
      <ul class="space-y-3">
        <li v-for="s in shoots.data.value" :key="s.id" class="card">
          <div class="mb-2 flex flex-wrap items-center gap-2 text-sm">
            <span class="text-sky-600" aria-hidden="true">●</span>
            <RouterLink :to="`/leaves/${s.leafId}`" class="font-medium hover:underline">
              {{ leafById.get(s.leafId) ? leafTitle(leafById.get(s.leafId)!) : 'Deleted task' }}
            </RouterLink>
            <Badge :tone="leafById.get(s.leafId)?.superseded ? 'warn' : 'neutral'">
              v{{ leafById.get(s.leafId)?.version ?? '?' }}
            </Badge>
            <span class="text-stone-500">{{ s.author || 'unknown' }} · {{ formatDate(s.createdAt) }}</span>
            <button class="btn btn-danger ml-auto" @click="deleteShoot(s.id)">Delete</button>
          </div>
          <MarkdownView :source="s.content" />
        </li>
      </ul>
    </section>

    <section>
      <h2 class="section-title">Attestations ({{ attestations.data.value?.length ?? 0 }})</h2>
      <ul class="space-y-3">
        <li v-for="a in attestations.data.value" :key="a.id">
          <AttestationCard
            :attestation="a"
            :node-name="nodeById.get(a.nodeId)?.displayName"
            :stale="nodeById.get(a.nodeId)?.superseded"
            @changed="reload"
          />
        </li>
      </ul>
    </section>
  </template>
</template>
