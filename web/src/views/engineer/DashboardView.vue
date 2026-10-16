<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../../api/client'
import { useAuthStore } from '../../stores/auth'
import type { Workspace } from '../../types/workspace'
import type { Status } from '../../types/status'
import type { PinProposal, ProcessorProposal } from '../../types/proposals'
import type { RunRecord } from '../../types/runs'

const route = useRoute()
const auth = useAuthStore()

const workspaces = ref<Workspace[]>([])
const status = ref<Status | null>(null)
const pinProposals = ref<PinProposal[]>([])
const processorProposals = ref<ProcessorProposal[]>([])
const failedRuns = ref<RunRecord[]>([])
const loadError = ref('')

const workspaceId = computed(() => (typeof route.params.id === 'string' ? route.params.id : null))

const visibleWorkspaces = computed(() => workspaces.value.filter((w) => auth.has('engineer', w.id)))

async function loadWorkspaces() {
  workspaces.value = await api.get<Workspace[]>('/api/workspaces')
}

async function loadDashboard(id: string) {
  loadError.value = ''
  try {
    const [s, pins, procs, runs] = await Promise.all([
      api.get<Status>(`/api/workspaces/${id}/status`),
      api.get<PinProposal[]>(`/api/workspaces/${id}/proposals`),
      api.get<ProcessorProposal[]>(`/api/workspaces/${id}/processor-proposals`),
      api.get<RunRecord[]>(`/api/workspaces/${id}/runs`),
    ])
    status.value = s
    pinProposals.value = pins
    processorProposals.value = procs
    failedRuns.value = runs.filter((r) => r.state === 'failed')
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err)
  }
}

const unanswered = computed(() => status.value?.tasks.filter((t) => t.state === 'unanswered') ?? [])
const pendingUpdate = computed(() => status.value?.tasks.filter((t) => t.state === 'pending-update') ?? [])
const answered = computed(() => status.value?.tasks.filter((t) => t.state === 'answered') ?? [])
const openProposalCount = computed(() => pinProposals.value.length + processorProposals.value.length)

onMounted(() => {
  loadWorkspaces()
  if (workspaceId.value) loadDashboard(workspaceId.value)
})

watch(workspaceId, (id) => {
  if (id) loadDashboard(id)
})
</script>

<template>
  <div v-if="!workspaceId" class="space-y-4">
    <h1 class="text-xl font-semibold">Workspaces</h1>
    <ul class="divide-y rounded border bg-white">
      <li v-for="w in visibleWorkspaces" :key="w.id">
        <router-link :to="`/w/${w.id}`" class="flex items-center justify-between px-4 py-3 hover:bg-gray-50">
          <span class="font-mono text-sm">{{ w.id }}</span>
          <span v-if="w.frozen" class="rounded bg-blue-100 px-2 py-0.5 text-xs text-blue-800">frozen</span>
        </router-link>
      </li>
      <li v-if="!visibleWorkspaces.length" class="px-4 py-3 text-sm text-gray-500">
        No workspace is assigned to you yet.
      </li>
    </ul>
  </div>

  <div v-else class="space-y-6">
    <h1 class="font-mono text-xl font-semibold">{{ workspaceId }}</h1>
    <p v-if="loadError" class="text-sm text-red-600">{{ loadError }}</p>

    <div class="grid grid-cols-2 gap-4 sm:grid-cols-4">
      <div class="rounded border bg-white p-4">
        <div class="text-2xl font-semibold">{{ unanswered.length }}</div>
        <div class="text-sm text-gray-500">unanswered</div>
      </div>
      <div class="rounded border bg-white p-4">
        <div class="text-2xl font-semibold">{{ pendingUpdate.length }}</div>
        <div class="text-sm text-gray-500">pending update</div>
      </div>
      <div class="rounded border bg-white p-4">
        <div class="text-2xl font-semibold">{{ openProposalCount }}</div>
        <div class="text-sm text-gray-500">
          <router-link :to="`/w/${workspaceId}/proposals`" class="hover:underline">open proposals</router-link>
        </div>
      </div>
      <div class="rounded border bg-white p-4">
        <div class="text-2xl font-semibold">{{ failedRuns.length }}</div>
        <div class="text-sm text-gray-500">failed runs</div>
      </div>
    </div>

    <section>
      <h2 class="mb-2 font-medium text-gray-700">Unanswered ({{ unanswered.length }})</h2>
      <ul class="divide-y rounded border bg-white">
        <li v-for="t in unanswered" :key="t.id">
          <router-link :to="`/w/${workspaceId}/tasks/${t.id}`" class="flex items-center justify-between px-4 py-2 hover:bg-gray-50">
            <span>{{ t.title }}</span>
            <span class="text-xs text-gray-400">{{ t.generated ? 'generated' : 'catalog' }}</span>
          </router-link>
        </li>
        <li v-if="!unanswered.length" class="px-4 py-2 text-sm text-gray-400">none</li>
      </ul>
    </section>

    <section>
      <h2 class="mb-2 font-medium text-gray-700">Pending update ({{ pendingUpdate.length }})</h2>
      <ul class="divide-y rounded border bg-white">
        <li v-for="t in pendingUpdate" :key="t.id">
          <router-link :to="`/w/${workspaceId}/tasks/${t.id}`" class="flex items-center justify-between px-4 py-2 hover:bg-gray-50">
            <span>{{ t.title }}</span>
            <span class="text-xs text-gray-400">{{ t.generated ? 'generated' : 'catalog' }}</span>
          </router-link>
        </li>
        <li v-if="!pendingUpdate.length" class="px-4 py-2 text-sm text-gray-400">none</li>
      </ul>
    </section>

    <section>
      <h2 class="mb-2 font-medium text-gray-700">Answered ({{ answered.length }})</h2>
      <ul class="divide-y rounded border bg-white">
        <li v-for="t in answered" :key="t.id">
          <router-link :to="`/w/${workspaceId}/tasks/${t.id}`" class="flex items-center justify-between px-4 py-2 hover:bg-gray-50">
            <span>{{ t.title }}</span>
            <span class="text-xs text-gray-400">{{ t.generated ? 'generated' : 'catalog' }}</span>
          </router-link>
        </li>
        <li v-if="!answered.length" class="px-4 py-2 text-sm text-gray-400">none</li>
      </ul>
    </section>
  </div>
</template>
