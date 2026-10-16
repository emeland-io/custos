<script setup lang="ts">
// Dry run (§5.7): run a candidate image over every answer bound to this
// processor and report what it would propose. Nothing is committed.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { errorMessage, getDryRun, listProcessors, startDryRun, statusOf } from '../../api/authors'
import { routeParam, useAuthorRole } from '../../router/authors'
import type { DryRunReport, ProcessorInfo } from '../../types/processorAuthor'

const POLL_MS = 2000
const MAX_POLL_ERRORS = 3
const PINNED_IMAGE_RE = /^[^\s@]+@sha256:[0-9a-f]{64}$/ // internal/runs/dryrun.go pinnedImageRE

type Phase = 'idle' | 'starting' | 'polling' | 'done' | 'forgotten' | 'stalled'

const route = useRoute()
const router = useRouter()
const allowed = useAuthorRole('processor_author')
const name = computed(() => routeParam(route.params.name))

const processor = ref<ProcessorInfo | null>(null)
const processorError = ref('')
const image = ref('')
const jobId = ref('')
const report = ref<DryRunReport | null>(null)
const phase = ref<Phase>('idle')
const startError = ref('')
const pollError = ref('')

const imageError = computed(() => {
  const s = image.value.trim()
  if (s === '' || PINNED_IMAGE_RE.test(s)) return ''
  return 'The image must be pinned by digest, as in registry.example.org/name@sha256:<64 hex digits>.'
})
const busy = computed(() => phase.value === 'starting' || phase.value === 'polling')

let timer: ReturnType<typeof setTimeout> | undefined
let token = 0
let pollErrors = 0

function stopPolling() {
  token++
  clearTimeout(timer)
  timer = undefined
}

function begin(id: string) {
  stopPolling()
  const mine = token
  jobId.value = id
  report.value = null
  pollError.value = ''
  pollErrors = 0
  phase.value = 'polling'
  void poll(id, mine)
}

async function poll(id: string, mine: number) {
  try {
    const r = await getDryRun(id)
    if (mine !== token) return
    pollErrors = 0
    pollError.value = ''
    report.value = r
    if (r.state === 'succeeded' || r.state === 'failed') {
      phase.value = 'done'
      return
    }
    timer = setTimeout(() => void poll(id, mine), POLL_MS)
  } catch (e) {
    if (mine !== token) return
    if (statusOf(e) === 404) {
      phase.value = 'forgotten'
      return
    }
    pollErrors++
    pollError.value = errorMessage(e)
    if (pollErrors >= MAX_POLL_ERRORS) {
      phase.value = 'stalled'
      return
    }
    timer = setTimeout(() => void poll(id, mine), POLL_MS)
  }
}

function stopWatching() {
  stopPolling()
  phase.value = 'idle'
}

async function start() {
  const img = image.value.trim()
  if (img === '' || imageError.value !== '' || busy.value) return
  phase.value = 'starting'
  startError.value = ''
  try {
    const { id } = await startDryRun(name.value, img)
    begin(id)
    await router.replace({ query: { ...route.query, job: id } })
  } catch (e) {
    phase.value = 'idle'
    startError.value = errorMessage(e)
  }
}

async function loadProcessor() {
  processor.value = null
  processorError.value = ''
  try {
    const p = (await listProcessors()).find((x) => x.name === name.value) ?? null
    processor.value = p
    if (p === null) processorError.value = `No processor "${name.value}" is registered on the catalog's main.`
    else if (image.value === '') image.value = p.image
  } catch (e) {
    processorError.value = errorMessage(e)
  }
}

watch([allowed, name], () => {
  stopPolling()
  phase.value = 'idle'
  report.value = null
  jobId.value = ''
  image.value = ''
  startError.value = ''
  if (!allowed.value) return
  void loadProcessor()
  const job = routeParam(route.query.job)
  if (job !== '') begin(job)
}, { immediate: true })

onBeforeUnmount(stopPolling)
</script>

<template>
  <section class="mx-auto max-w-5xl space-y-6 p-6">
    <p v-if="!allowed" class="text-gray-700">Dry runs need the processor-author role.</p>
    <template v-else>
      <nav class="space-x-4 text-sm">
        <RouterLink to="/processors" class="text-blue-700 hover:underline">← Processors</RouterLink>
        <RouterLink :to="`/processors/${name}/runs`" class="text-blue-700 hover:underline">Runs</RouterLink>
      </nav>
      <header class="space-y-1">
        <h1 class="text-2xl font-semibold">Dry run of <span class="font-mono">{{ name }}</span></h1>
        <p class="text-sm text-gray-600">
          Runs a candidate image over every answer bound to this processor, in every workspace, and reports how many
          proposals it would open. Nothing is committed.
        </p>
      </header>

      <p v-if="processorError" class="text-red-700">{{ processorError }}</p>
      <p v-else-if="processor" class="text-sm text-gray-700">
        Registered image: <code class="break-all">{{ processor.image }}</code> ·
        {{ processor.bound_tasks.length }} bound {{ processor.bound_tasks.length === 1 ? 'task' : 'tasks' }}
      </p>

      <form class="space-y-2" @submit.prevent="start">
        <label class="block text-sm font-semibold">
          Candidate image
          <input
            v-model="image"
            type="text"
            class="mt-1 w-full rounded border border-gray-300 px-3 py-2 font-mono text-sm font-normal"
            :disabled="busy"
          />
        </label>
        <p v-if="imageError" class="text-sm text-red-700">{{ imageError }}</p>
        <div class="flex gap-2">
          <button
            type="submit"
            class="rounded bg-blue-600 px-4 py-2 text-white disabled:opacity-50"
            :disabled="busy || image.trim() === '' || imageError !== ''"
          >
            {{ phase === 'starting' ? 'Starting…' : 'Start dry run' }}
          </button>
          <button
            v-if="phase === 'polling'"
            type="button"
            class="rounded px-4 py-2 text-gray-700 hover:bg-gray-100"
            @click="stopWatching"
          >
            Stop watching
          </button>
        </div>
        <p v-if="startError" class="text-red-700">{{ startError }}</p>
      </form>

      <section v-if="jobId !== ''" class="space-y-3 border-t pt-4">
        <h2 class="font-semibold">Job <span class="font-mono">{{ jobId.slice(0, 8) }}</span></h2>
        <p v-if="phase === 'forgotten'" class="text-amber-800">
          The server no longer knows this dry run; dry runs are kept in memory only and are lost when the server
          restarts. Start it again.
        </p>
        <p v-if="phase === 'stalled'" class="text-red-700">
          Polling stopped after {{ MAX_POLL_ERRORS }} failed attempts: {{ pollError }}
          <button type="button" class="ml-2 text-blue-700 hover:underline" @click="begin(jobId)">Resume polling</button>
        </p>
        <p v-else-if="pollError" class="text-sm text-amber-800">Retrying after an error: {{ pollError }}</p>

        <template v-if="report">
          <p>
            <span
              class="rounded px-1.5 text-xs"
              :class="report.state === 'running' ? 'bg-blue-100 text-blue-900' : report.state === 'succeeded' ? 'bg-green-100 text-green-900' : 'bg-red-100 text-red-900'"
            >{{ report.state }}</span>
            <span v-if="phase === 'polling'" class="ml-2 text-xs text-gray-600">refreshing every {{ POLL_MS / 1000 }} s</span>
          </p>
          <p v-if="report.error" class="whitespace-pre-wrap rounded bg-red-50 p-2 text-red-900">{{ report.error }}</p>
          <p>
            Ran {{ report.runs }} bound {{ report.runs === 1 ? 'answer' : 'answers' }} ({{ report.failed }} failed); would open
            {{ report.proposals }} {{ report.proposals === 1 ? 'proposal' : 'proposals' }} in {{ report.workspaces }}
            {{ report.workspaces === 1 ? 'workspace' : 'workspaces' }}.
          </p>
          <div v-if="report.samples.length > 0" class="space-y-3">
            <h3 class="font-semibold">Samples</h3>
            <article v-for="s in report.samples" :key="s.workspace + '/' + s.task" class="rounded border border-gray-200 p-3 text-sm">
              <p class="mb-2 text-gray-700">
                Workspace <span class="font-mono">{{ s.workspace.slice(0, 8) }}</span>, task
                <span class="font-mono">{{ s.task.slice(0, 8) }}</span>
              </p>
              <table class="w-full text-left">
                <thead>
                  <tr class="border-b">
                    <th class="py-1">Kind</th>
                    <th>Change</th>
                    <th>Title</th>
                    <th>Versions</th>
                    <th>Match key</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="it in s.items" :key="it.kind + ':' + it.match_key" class="border-b">
                    <td class="py-1">{{ it.kind }}</td>
                    <td>{{ it.action }}</td>
                    <td>{{ it.title }}</td>
                    <td class="font-mono">{{ it.from ?? '' }}{{ it.from && it.to ? ' → ' : '' }}{{ it.to ?? '' }}</td>
                    <td class="font-mono">{{ it.match_key }}</td>
                  </tr>
                </tbody>
              </table>
            </article>
          </div>
        </template>
        <p v-else-if="phase === 'polling'" class="text-gray-600">Waiting for the first report…</p>
      </section>
    </template>
  </section>
</template>
