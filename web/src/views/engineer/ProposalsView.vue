<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api, ApiError } from '../../api/client'
import type { PinProposal, ProcessorProposal, ProposalItem } from '../../types/proposals'

const route = useRoute()
const workspaceId = computed(() => route.params.id as string)

const pinProposals = ref<PinProposal[]>([])
const processorProposals = ref<ProcessorProposal[]>([])
const errorMessage = ref('')
const selected = ref<Record<string, Set<string>>>({}) // branch -> set of "kind:match_key" selectors
const busy = ref<Record<string, boolean>>({})

// selector matches internal/proposal/diff.go's selector: "<kind>:<match_key>".
function selector(item: ProposalItem): string {
  return `${item.kind}:${item.match_key}`
}

function selectAll(p: ProcessorProposal) {
  selected.value[p.branch] = new Set(p.items.map(selector))
}

function toggle(p: ProcessorProposal, item: ProposalItem) {
  const set = selected.value[p.branch] ?? new Set<string>()
  const key = selector(item)
  if (set.has(key)) set.delete(key)
  else set.add(key)
  selected.value[p.branch] = set
}

function noneSelected(p: ProcessorProposal): boolean {
  return !selected.value[p.branch]?.size
}

async function load() {
  errorMessage.value = ''
  try {
    const [pins, procs] = await Promise.all([
      api.get<PinProposal[]>(`/api/workspaces/${workspaceId.value}/proposals`),
      api.get<ProcessorProposal[]>(`/api/workspaces/${workspaceId.value}/processor-proposals`),
    ])
    pinProposals.value = pins
    processorProposals.value = procs
    for (const p of procs) selectAll(p)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  }
}

onMounted(load)

function reportError(err: unknown) {
  errorMessage.value = err instanceof ApiError ? err.body.error : err instanceof Error ? err.message : String(err)
}

async function acceptPin(p: PinProposal) {
  busy.value[p.branch] = true
  try {
    await api.post(`/api/workspaces/${workspaceId.value}/proposals/accept`, { branch: p.branch })
    await load()
  } catch (err) {
    reportError(err)
  } finally {
    busy.value[p.branch] = false
  }
}

async function rejectPin(p: PinProposal) {
  busy.value[p.branch] = true
  try {
    await api.post(`/api/workspaces/${workspaceId.value}/proposals/reject`, { branch: p.branch })
    await load()
  } catch (err) {
    reportError(err)
  } finally {
    busy.value[p.branch] = false
  }
}

async function acceptProcessor(p: ProcessorProposal) {
  busy.value[p.branch] = true
  try {
    const items = Array.from(selected.value[p.branch] ?? [])
    await api.post(`/api/workspaces/${workspaceId.value}/processor-proposals/${p.task}/accept`, { items })
    await load()
  } catch (err) {
    reportError(err)
  } finally {
    busy.value[p.branch] = false
  }
}

async function rejectProcessor(p: ProcessorProposal) {
  busy.value[p.branch] = true
  try {
    await api.post(`/api/workspaces/${workspaceId.value}/processor-proposals/${p.task}/reject`)
    await load()
  } catch (err) {
    reportError(err)
  } finally {
    busy.value[p.branch] = false
  }
}
</script>

<template>
  <div class="space-y-8">
    <p v-if="errorMessage" class="text-sm text-red-600">{{ errorMessage }}</p>

    <section>
      <h1 class="mb-3 text-xl font-semibold">Catalog pin updates</h1>
      <div v-if="!pinProposals.length" class="text-sm text-gray-500">No open pin proposals.</div>
      <div v-for="p in pinProposals" :key="p.branch" class="mb-3 rounded border bg-white p-4">
        <p class="font-mono text-xs text-gray-500">{{ p.branch }}</p>
        <ul class="my-2 text-sm">
          <li>{{ p.changes.added.length }} added</li>
          <li>{{ p.changes.new_versions.length }} new versions</li>
          <li>{{ p.changes.superseded.length }} superseded</li>
          <li v-if="p.changes.groups_changed">groups changed</li>
          <li v-if="p.changes.bindings_changed">processor bindings changed</li>
        </ul>
        <button
          class="rounded bg-gray-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
          :disabled="busy[p.branch]"
          @click="acceptPin(p)"
        >
          Accept
        </button>
        <button class="ml-2 text-sm text-gray-600 hover:text-red-600" :disabled="busy[p.branch]" @click="rejectPin(p)">
          Reject
        </button>
      </div>
    </section>

    <section>
      <h1 class="mb-3 text-xl font-semibold">Processor output proposals</h1>
      <div v-if="!processorProposals.length" class="text-sm text-gray-500">No open processor proposals.</div>
      <div v-for="p in processorProposals" :key="p.branch" class="mb-3 rounded border bg-white p-4">
        <p class="font-mono text-xs text-gray-500">task {{ p.task }} · {{ p.cascade ? 'cascade removal' : 'output' }}</p>
        <ul class="my-2 divide-y text-sm">
          <li v-for="item in p.items" :key="selector(item)" class="flex items-center gap-2 py-1">
            <input type="checkbox" :checked="selected[p.branch]?.has(selector(item))" @change="toggle(p, item)" />
            <span class="w-20 shrink-0 text-xs uppercase text-gray-400">{{ item.action }}</span>
            <span class="w-16 shrink-0 text-xs text-gray-400">{{ item.kind }}</span>
            <span class="flex-1">{{ item.title }}</span>
            <span
              v-if="item.verification"
              class="text-xs"
              :class="{
                'text-green-700': item.verification.status === 'verified',
                'text-red-700': item.verification.status === 'failed',
                'text-gray-500': item.verification.status === 'unsigned',
              }"
            >
              {{ item.verification.status }}
            </span>
          </li>
        </ul>
        <p v-if="noneSelected(p)" class="mb-2 text-xs text-amber-700">
          Select at least one item, or reject the whole proposal.
        </p>
        <button
          class="rounded bg-gray-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
          :disabled="busy[p.branch] || noneSelected(p)"
          @click="acceptProcessor(p)"
        >
          Accept selected
        </button>
        <button
          class="ml-2 text-sm text-gray-600 hover:text-red-600"
          :disabled="busy[p.branch]"
          @click="rejectProcessor(p)"
        >
          Reject
        </button>
      </div>
    </section>
  </div>
</template>
