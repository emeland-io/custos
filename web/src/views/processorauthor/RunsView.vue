<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { errorMessage, getRunLog, listRuns, listWorkspaces, retryRun } from '../../api/authors'
import { routeParam, useAuthorRole } from '../../router/authors'
import type { RunRecord, RunState } from '../../types/processorAuthor'

const POLL_MS = 5000
const STATES: RunState[] = ['queued', 'running', 'succeeded', 'failed']

const route = useRoute()
const allowed = useAuthorRole('processor_author')
const name = computed(() => routeParam(route.params.name))

const runs = ref<RunRecord[]>([])
const workspaceErrors = ref<string[]>([])
const error = ref('')
const loading = ref(false)
const polling = ref(false)
const stateFilter = ref<RunState | ''>('')
const workspaceFilter = ref('')
const selected = ref<RunRecord | null>(null)
const log = ref('')
const logError = ref('')
const logLoading = ref(false)
const retrying = ref(false)
const actionError = ref('')

let timer: ReturnType<typeof setTimeout> | undefined
let alive = true
let refreshSeq = 0

const active = (r: RunRecord) => r.state === 'queued' || r.state === 'running'
const workspaces = computed(() => [...new Set(runs.value.map((r) => r.workspace))].sort())
const shown = computed(() =>
  runs.value.filter(
    (r) =>
      (stateFilter.value === '' || r.state === stateFilter.value) &&
      (workspaceFilter.value === '' || r.workspace === workspaceFilter.value),
  ),
)
const fmt = (s: string | undefined) => (s ? new Date(s).toLocaleString() : '—')
const stateClass = (s: RunState) =>
  ({
    queued: 'bg-gray-100 text-gray-800',
    running: 'bg-blue-100 text-blue-900',
    succeeded: 'bg-green-100 text-green-900',
    failed: 'bg-red-100 text-red-900',
  })[s]

async function loadLog(r: RunRecord) {
  logLoading.value = true
  logError.value = ''
  try {
    const text = await getRunLog(r.workspace, r.id)
    if (selected.value?.id === r.id) log.value = text
  } catch (e) {
    if (selected.value?.id === r.id) logError.value = errorMessage(e)
  } finally {
    logLoading.value = false
  }
}

function select(r: RunRecord) {
  selected.value = r
  log.value = ''
  actionError.value = ''
  void loadLog(r)
}

async function refresh() {
  clearTimeout(timer)
  timer = undefined
  polling.value = false
  if (!allowed.value || name.value === '') return
  const seq = ++refreshSeq
  loading.value = runs.value.length === 0
  error.value = ''
  try {
    const wss = await listWorkspaces()
    const results = await Promise.allSettled(wss.map((w) => listRuns(w.id)))
    if (!alive || seq !== refreshSeq) return
    const all: RunRecord[] = []
    const errs: string[] = []
    results.forEach((r, i) => {
      if (r.status === 'fulfilled') all.push(...r.value.filter((x) => x.processor === name.value))
      else errs.push(`${wss[i]?.id ?? '?'}: ${errorMessage(r.reason)}`)
    })
    all.sort((a, b) => Date.parse(b.queued) - Date.parse(a.queued))
    runs.value = all
    workspaceErrors.value = errs
    const sel = selected.value
    if (sel !== null) {
      const now = all.find((r) => r.id === sel.id && r.workspace === sel.workspace) ?? null
      selected.value = now
      if (now === null) log.value = ''
      else if (now.state !== sel.state) void loadLog(now)
    }
    if (all.some(active)) {
      polling.value = true
      timer = setTimeout(() => void refresh(), POLL_MS)
    }
  } catch (e) {
    if (alive && seq === refreshSeq) error.value = errorMessage(e)
  } finally {
    if (seq === refreshSeq) loading.value = false
  }
}

async function retry(r: RunRecord) {
  retrying.value = true
  actionError.value = ''
  try {
    const rec = await retryRun(r.workspace, r.id)
    await refresh()
    const now = runs.value.find((x) => x.id === rec.id && x.workspace === rec.workspace)
    if (now) select(now)
  } catch (e) {
    actionError.value = errorMessage(e)
  } finally {
    retrying.value = false
  }
}

watch([allowed, name], () => {
  selected.value = null
  runs.value = []
  void refresh()
}, { immediate: true })

onBeforeUnmount(() => {
  alive = false
  clearTimeout(timer)
})
</script>

<template>
  <section class="mx-auto max-w-7xl space-y-6 p-6">
    <p v-if="!allowed" class="text-gray-700">Run history needs the processor-author role.</p>
    <template v-else>
      <nav class="space-x-4 text-sm">
        <RouterLink to="/processors" class="text-blue-700 hover:underline">← Processors</RouterLink>
        <RouterLink :to="`/processors/${name}/dry-run`" class="text-blue-700 hover:underline">Dry run</RouterLink>
      </nav>
      <header class="flex flex-wrap items-baseline justify-between gap-2">
        <h1 class="text-2xl font-semibold">Runs of <span class="font-mono">{{ name }}</span></h1>
        <span v-if="polling" class="text-xs text-gray-600">Refreshing every {{ POLL_MS / 1000 }} s while runs are queued or running.</span>
      </header>

      <div class="flex flex-wrap gap-4 text-sm">
        <label>
          State
          <select v-model="stateFilter" class="ml-1 rounded border border-gray-300 px-2 py-1">
            <option value="">all</option>
            <option v-for="s in STATES" :key="s" :value="s">{{ s }}</option>
          </select>
        </label>
        <label>
          Workspace
          <select v-model="workspaceFilter" class="ml-1 rounded border border-gray-300 px-2 py-1 font-mono">
            <option value="">all</option>
            <option v-for="w in workspaces" :key="w" :value="w">{{ w.slice(0, 8) }}</option>
          </select>
        </label>
        <button type="button" class="rounded px-3 py-1 text-blue-700 hover:bg-gray-100" @click="refresh">Refresh</button>
      </div>

      <p v-if="error" class="text-red-700">{{ error }}</p>
      <div v-if="workspaceErrors.length > 0" class="text-sm text-amber-800">
        Could not load runs of:
        <ul class="list-disc pl-6 font-mono">
          <li v-for="w in workspaceErrors" :key="w">{{ w }}</li>
        </ul>
      </div>

      <div class="grid gap-6 lg:grid-cols-[3fr_2fr]">
        <div>
          <p v-if="loading" class="text-gray-600">Loading…</p>
          <p v-else-if="shown.length === 0" class="text-gray-600">No runs.</p>
          <table v-else class="w-full text-left text-sm">
            <thead>
              <tr class="border-b">
                <th class="py-2">Queued</th>
                <th>Workspace</th>
                <th>Task</th>
                <th>Reason</th>
                <th>State</th>
                <th>Outcome</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="r in shown"
                :key="r.workspace + '/' + r.id"
                tabindex="0"
                class="cursor-pointer border-b hover:bg-gray-50"
                :class="{ 'bg-blue-50': selected?.id === r.id && selected?.workspace === r.workspace }"
                @click="select(r)"
                @keydown.enter="select(r)"
              >
                <td class="py-2">{{ fmt(r.queued) }}</td>
                <td class="font-mono">{{ r.workspace.slice(0, 8) }}</td>
                <td class="font-mono">{{ r.task.id.slice(0, 8) }}@{{ r.task.version }}</td>
                <td>{{ r.reason }}</td>
                <td><span class="rounded px-1.5 text-xs" :class="stateClass(r.state)">{{ r.state }}</span></td>
                <td>{{ r.outcome ?? '' }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <aside v-if="selected" class="space-y-3 rounded border border-gray-200 p-4 text-sm">
          <h2 class="font-semibold">Run <span class="font-mono">{{ selected.id.slice(0, 8) }}</span></h2>
          <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
            <dt class="text-gray-600">Workspace</dt><dd class="break-all font-mono">{{ selected.workspace }}</dd>
            <dt class="text-gray-600">Task</dt><dd class="break-all font-mono">{{ selected.task.id }}@{{ selected.task.version }}</dd>
            <dt class="text-gray-600">Answer</dt><dd class="break-all font-mono">{{ selected.answer_path }}</dd>
            <dt class="text-gray-600">Image</dt><dd class="break-all font-mono">{{ selected.image }}</dd>
            <dt class="text-gray-600">Reason</dt><dd>{{ selected.reason }}<span v-if="selected.retry_of"> (retry of {{ selected.retry_of.slice(0, 8) }})</span></dd>
            <dt class="text-gray-600">State</dt><dd>{{ selected.state }}<span v-if="selected.outcome">, {{ selected.outcome }}</span></dd>
            <dt v-if="selected.branch" class="text-gray-600">Proposal</dt><dd v-if="selected.branch" class="break-all font-mono">{{ selected.branch }}</dd>
            <dt class="text-gray-600">Queued</dt><dd>{{ fmt(selected.queued) }}</dd>
            <dt class="text-gray-600">Started</dt><dd>{{ fmt(selected.started) }}</dd>
            <dt class="text-gray-600">Finished</dt><dd>{{ fmt(selected.finished) }}</dd>
          </dl>
          <p v-if="selected.error" class="whitespace-pre-wrap rounded bg-red-50 p-2 text-red-900">{{ selected.error }}</p>
          <button
            v-if="selected.state === 'failed'"
            type="button"
            class="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-50"
            :disabled="retrying"
            @click="retry(selected)"
          >
            {{ retrying ? 'Retrying…' : 'Retry' }}
          </button>
          <p v-if="actionError" class="text-red-700">{{ actionError }}</p>
          <h3 class="font-semibold">Log</h3>
          <p v-if="logLoading" class="text-gray-600">Loading the log…</p>
          <p v-else-if="logError" class="text-red-700">{{ logError }}</p>
          <pre v-else-if="log !== ''" class="max-h-96 overflow-auto whitespace-pre-wrap rounded bg-gray-900 p-3 text-xs text-gray-100">{{ log }}</pre>
          <p v-else class="text-gray-600">(empty log)</p>
        </aside>
      </div>
    </template>
  </section>
</template>
