<script setup lang="ts">
// A task's version history (§3.1 "task-history graph") as an ordered list
// of generations — plain HTML, no graph-drawing library (ruling 4.16).
import { computed, ref, watch } from 'vue'
import { compareVersions } from './semver'
import { errorMessage, getCatalogTask, statusOf } from '../../api/authors'
import type { CatalogTaskVersion, TaskHistory, TaskRef } from '../../types/taskAuthor'

const props = defineProps<{
  taskId: string
  draftVersion: CatalogTaskVersion | null
}>()

interface GraphNode {
  version: CatalogTaskVersion
  generation: number
  mainCurrent: boolean
  draftCurrent: boolean
  draftOnly: boolean
}

const history = ref<TaskHistory | null>(null)
const notOnMain = ref(false)
const error = ref('')
const loading = ref(false)

const refKey = (r: TaskRef) => `${r.id}@${r.version}`

async function load() {
  history.value = null
  notOnMain.value = false
  error.value = ''
  if (props.taskId === '') return
  loading.value = true
  try {
    history.value = await getCatalogTask(props.taskId)
  } catch (e) {
    if (statusOf(e) === 404) notOnMain.value = true
    else error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}

watch(() => props.taskId, load, { immediate: true })

/** Published versions of this task, by "id@version". */
const published = computed(() => {
  const m = new Map<string, CatalogTaskVersion>()
  for (const v of history.value?.versions ?? []) m.set(refKey(v), v)
  return m
})

const nodes = computed<GraphNode[]>(() => {
  const shown = new Map(published.value)
  const d = props.draftVersion
  const draftOnly = d !== null && !shown.has(refKey(d))
  if (d !== null && draftOnly) shown.set(refKey(d), d)

  const gen = new Map<string, number>()
  const visiting = new Set<string>()
  const generation = (k: string): number => {
    const cached = gen.get(k)
    if (cached !== undefined) return cached
    const v = shown.get(k)
    if (v === undefined || visiting.has(k)) return -1 // not shown, or a cycle
    visiting.add(k)
    let g = 0
    for (const p of v.previous) g = Math.max(g, generation(refKey(p)) + 1)
    visiting.delete(k)
    gen.set(k, g)
    return g
  }

  const out: GraphNode[] = []
  let maxPublished = -1
  for (const [k, v] of published.value) {
    const g = generation(k)
    maxPublished = Math.max(maxPublished, g)
    out.push({
      version: v,
      generation: g,
      mainCurrent: v.version === history.value?.current,
      draftCurrent: d !== null && refKey(d) === k,
      draftOnly: false,
    })
  }
  if (d !== null && draftOnly) {
    let g = generation(refKey(d))
    if (d.previous.some((p) => p.id === d.id && !shown.has(refKey(p)))) g = Math.max(g, maxPublished + 1)
    out.push({ version: d, generation: g, mainCurrent: false, draftCurrent: true, draftOnly: true })
  }
  return out
})

const generations = computed(() => {
  const by = new Map<number, GraphNode[]>()
  for (const n of nodes.value) {
    const list = by.get(n.generation) ?? []
    list.push(n)
    by.set(n.generation, list)
  }
  return [...by.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([generation, list]) => ({
      generation,
      nodes: list.sort((x, y) => compareVersions(x.version.version, y.version.version)),
    }))
})

function previousLabel(p: TaskRef): string {
  if (p.id === props.taskId) {
    return published.value.has(refKey(p)) ? p.version : `${p.version} (not published; an earlier version in this draft)`
  }
  return `${p.id.slice(0, 8)}…@${p.version} (another task, merged into this one)`
}
</script>

<template>
  <section class="space-y-3">
    <h2 class="text-lg font-semibold">Version history</h2>
    <p v-if="loading" class="text-sm text-gray-600">Loading…</p>
    <p v-else-if="error" class="text-sm text-red-700">{{ error }}</p>
    <template v-else>
      <p v-if="notOnMain" class="text-sm text-gray-600">
        This task is not in the published catalog yet; only this draft's version is shown.
      </p>
      <p v-if="history?.superseded" class="text-sm text-amber-800">
        In the published catalog this task was merged into another task and has no current version.
      </p>
      <ol class="space-y-3">
        <li v-for="g in generations" :key="g.generation" class="rounded border border-gray-200 p-3">
          <h3 class="text-xs font-semibold uppercase text-gray-500">Generation {{ g.generation + 1 }}</h3>
          <ul class="mt-2 space-y-2">
            <li v-for="n in g.nodes" :key="n.version.version" class="text-sm">
              <span class="font-mono font-semibold">{{ n.version.version }}</span>
              <span class="ml-2">{{ n.version.title }}</span>
              <span v-if="n.mainCurrent" class="ml-2 rounded bg-green-100 px-1.5 text-xs text-green-900">current (published)</span>
              <span v-if="n.draftCurrent" class="ml-2 rounded bg-blue-100 px-1.5 text-xs text-blue-900">current in this draft</span>
              <span v-if="n.draftOnly" class="ml-2 rounded bg-amber-100 px-1.5 text-xs text-amber-900">not published</span>
              <ul v-if="n.version.previous.length > 0" class="ml-6 text-xs text-gray-600">
                <li v-for="p in n.version.previous" :key="refKey(p)">← {{ previousLabel(p) }}</li>
              </ul>
            </li>
          </ul>
        </li>
      </ol>
    </template>
  </section>
</template>
