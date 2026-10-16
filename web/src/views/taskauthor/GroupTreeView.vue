<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import ProblemList from '../../components/ProblemList.vue'
import ReorderList from './ReorderList.vue'
import { errorMessage, getDraft, problemsOf, putGroup, putGroupsIndex, statusOf } from '../../api/authors'
import { routeParam, useAuthorRole } from '../../router/authors'
import type { CatalogGroup, CatalogProblem, DraftDetail, GroupChildRef } from '../../types/taskAuthor'

/** A top-level group in the index list; key is unique even if a draft lists a slug twice. */
interface IndexItem {
  key: string
  slug: string
}

/** A child of the selected group: exactly one of group (a slug) or task (a UUID). */
interface ChildItem {
  key: string
  group?: string
  task?: string
}

const SLUG_RE = /^[a-z0-9][a-z0-9-]*$/ // internal/catalog/catalog.go slugRE

const route = useRoute()
const router = useRouter()
const allowed = useAuthorRole('task_author')
const draftId = computed(() => routeParam(route.params.draftId))
const slug = computed(() => routeParam(route.params.slug))

const detail = ref<DraftDetail | null>(null)
const loadError = ref('')
const indexItems = ref<IndexItem[]>([])
const indexDirty = ref(false)
const title = ref('')
const children = ref<ChildItem[]>([])
const groupDirty = ref(false)
const saving = ref(false)
const saveError = ref('')
const saveProblems = ref<CatalogProblem[]>([])
const notice = ref('')
const addTaskId = ref('')
const newSlug = ref('')
const newTitle = ref('')
let keySeq = 0

const indexKey = (i: IndexItem) => i.key
const childKey = (c: ChildItem) => c.key

function collect(gs: CatalogGroup[], out: Map<string, CatalogGroup>): Map<string, CatalogGroup> {
  for (const g of gs) {
    if (out.has(g.slug)) continue
    out.set(g.slug, g)
    collect(g.children.flatMap((c) => (c.group ? [c.group] : [])), out)
  }
  return out
}

/** Every group reachable from the index, by slug. */
const groups = computed(() => collect(detail.value?.catalog.groups ?? [], new Map<string, CatalogGroup>()))
const selected = computed(() => groups.value.get(slug.value) ?? null)
const taskLabel = computed(
  () => new Map((detail.value?.catalog.tasks ?? []).map((t) => [t.id, `${t.title} (${t.version})`] as const)),
)
const groupTitle = (s: string) => groups.value.get(s)?.title ?? s

/** Slugs from a top-level group down to the selected one. */
const breadcrumb = computed(() => {
  const path: string[] = []
  const walk = (gs: CatalogGroup[]): boolean => {
    for (const g of gs) {
      path.push(g.slug)
      if (g.slug === slug.value) return true
      if (walk(g.children.flatMap((c) => (c.group ? [c.group] : [])))) return true
      path.pop()
    }
    return false
  }
  return walk(detail.value?.catalog.groups ?? []) ? path : []
})

/** Tasks in no group, counting the selected group's unsaved children instead of its saved ones. */
const ungroupedTasks = computed(() => {
  const used = new Set<string>()
  for (const g of groups.value.values()) {
    if (g.slug === slug.value) continue
    for (const c of g.children) if (c.task) used.add(c.task)
  }
  for (const c of children.value) if (c.task) used.add(c.task)
  return (detail.value?.catalog.tasks ?? [])
    .filter((t) => !used.has(t.id))
    .sort((a, b) => a.title.localeCompare(b.title))
})

const newSlugError = computed(() => {
  const s = newSlug.value.trim()
  if (s === '') return ''
  if (!SLUG_RE.test(s)) return 'A slug has lowercase letters, digits and dashes, and starts with a letter or digit.'
  if (groups.value.has(s)) return `There is already a group "${s}".`
  return ''
})

function resetIndex() {
  indexItems.value = (detail.value?.catalog.groups ?? []).map((g) => ({ key: `i${keySeq++}`, slug: g.slug }))
  indexDirty.value = false
}

function resetGroup() {
  const g = selected.value
  title.value = g?.title ?? ''
  children.value = (g?.children ?? []).map(
    (c): ChildItem => (c.group ? { key: `c${keySeq++}`, group: c.group.slug } : { key: `c${keySeq++}`, task: c.task ?? '' }),
  )
  addTaskId.value = ''
  groupDirty.value = false
}

/** Re-reads the draft; scope says which local edits to replace with what was read. */
async function load(scope: 'all' | 'index' | 'group' = 'all') {
  loadError.value = ''
  if (!allowed.value || draftId.value === '') return
  try {
    detail.value = await getDraft(draftId.value)
    if (scope !== 'group') resetIndex()
    if (scope !== 'index') resetGroup()
  } catch (e) {
    detail.value = null
    loadError.value =
      statusOf(e) === 404 ? 'This draft does not exist; it may have been published or discarded.' : errorMessage(e)
  }
}

watch([allowed, draftId, slug], () => {
  notice.value = ''
  saveError.value = ''
  saveProblems.value = []
  void load()
}, { immediate: true })

const dirty = computed(() => indexDirty.value || groupDirty.value)
const confirmLeave = () => !dirty.value || window.confirm('Discard your unsaved changes to the group tree?')
onBeforeRouteLeave(confirmLeave)
onBeforeRouteUpdate(confirmLeave)

function onIndexReordered(next: IndexItem[]) {
  indexItems.value = next
  indexDirty.value = true
}

function onChildrenReordered(next: ChildItem[]) {
  children.value = next
  groupDirty.value = true
}

function removeChild(key: string) {
  children.value = children.value.filter((c) => c.key !== key)
  groupDirty.value = true
}

function addTask() {
  if (addTaskId.value === '') return
  children.value = [...children.value, { key: `c${keySeq++}`, task: addTaskId.value }]
  addTaskId.value = ''
  groupDirty.value = true
}

async function save(done: string, action: () => Promise<void>) {
  saving.value = true
  saveError.value = ''
  saveProblems.value = []
  notice.value = ''
  try {
    await action()
    notice.value = done
  } catch (e) {
    saveError.value = errorMessage(e)
    saveProblems.value = problemsOf(e)
  } finally {
    saving.value = false
  }
}

function saveIndex() {
  return save('The top-level order is saved in this draft.', async () => {
    await putGroupsIndex(draftId.value, indexItems.value.map((i) => i.slug))
    await load('index')
  })
}

function saveGroup() {
  const t = title.value.trim()
  const body = {
    title: t,
    children: children.value.map((c): GroupChildRef => (c.group !== undefined ? { group: c.group } : { task: c.task })),
  }
  return save(`The group "${t}" is saved in this draft.`, async () => {
    await putGroup(draftId.value, slug.value, body)
    await load('group')
  })
}

function createGroup() {
  const s = newSlug.value.trim()
  const t = newTitle.value.trim()
  if (s === '' || t === '' || newSlugError.value !== '' || indexDirty.value) return
  return save(`The group "${t}" is created in this draft.`, async () => {
    await putGroup(draftId.value, s, { title: t, children: [] })
    await putGroupsIndex(draftId.value, [...indexItems.value.map((i) => i.slug), s])
    newSlug.value = ''
    newTitle.value = ''
    const failure = await router.push(`/catalog/drafts/${draftId.value}/groups/${s}`)
    if (failure) await load('index') // the user kept unsaved edits of the current group
  })
}
</script>

<template>
  <section class="mx-auto max-w-6xl space-y-6 p-6">
    <p v-if="!allowed" class="text-gray-700">Editing the group tree needs the task-author role.</p>
    <template v-else>
      <nav class="text-sm">
        <RouterLink :to="`/catalog/drafts/${draftId}`" class="text-blue-700 hover:underline">← Back to the draft</RouterLink>
      </nav>
      <p v-if="loadError" class="text-red-700">{{ loadError }}</p>
      <template v-else-if="detail">
        <h1 class="text-2xl font-semibold">Group tree of "{{ detail.draft.title }}"</h1>
        <p v-if="notice" class="rounded bg-green-50 p-2 text-green-900">{{ notice }}</p>
        <p v-if="saveError" class="text-red-700">{{ saveError }}</p>
        <ProblemList v-if="saveProblems.length > 0" :problems="saveProblems" />

        <div class="grid gap-6 md:grid-cols-[1fr_2fr]">
          <section class="space-y-3">
            <h2 class="font-semibold">Top-level order</h2>
            <p class="text-xs text-gray-600">Drag a group, or use the arrows, then save.</p>
            <ReorderList :items="indexItems" :item-key="indexKey" :disabled="saving" @update:items="onIndexReordered">
              <template #item="{ item }">
                <RouterLink
                  :to="`/catalog/drafts/${draftId}/groups/${item.slug}`"
                  :class="item.slug === slug ? 'font-semibold' : 'text-blue-700 hover:underline'"
                >
                  {{ groupTitle(item.slug) }}
                </RouterLink>
                <span class="ml-1 font-mono text-xs text-gray-500">{{ item.slug }}</span>
              </template>
            </ReorderList>
            <p v-if="indexItems.length === 0" class="text-sm text-gray-600">This catalog has no groups yet.</p>
            <div class="flex gap-2">
              <button
                type="button"
                class="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-50"
                :disabled="saving || !indexDirty"
                @click="saveIndex"
              >
                Save order
              </button>
              <button
                type="button"
                class="rounded px-3 py-1 text-gray-700 hover:bg-gray-100 disabled:opacity-50"
                :disabled="saving || !indexDirty"
                @click="resetIndex"
              >
                Discard
              </button>
            </div>

            <form class="space-y-2 border-t pt-3" @submit.prevent="createGroup">
              <h3 class="text-sm font-semibold">New top-level group</h3>
              <input v-model="newSlug" type="text" placeholder="slug, e.g. operations" class="w-full rounded border border-gray-300 px-2 py-1 font-mono text-sm" />
              <input v-model="newTitle" type="text" placeholder="Title" class="w-full rounded border border-gray-300 px-2 py-1 text-sm" />
              <p v-if="newSlugError" class="text-xs text-red-700">{{ newSlugError }}</p>
              <p v-if="indexDirty" class="text-xs text-amber-800">Save or discard the new order first.</p>
              <button
                type="submit"
                class="rounded bg-blue-600 px-3 py-1 text-sm text-white disabled:opacity-50"
                :disabled="saving || indexDirty || newSlug.trim() === '' || newTitle.trim() === '' || newSlugError !== ''"
              >
                Create group
              </button>
            </form>
          </section>

          <section class="space-y-3">
            <p v-if="!selected" class="text-gray-600">
              {{ slug === '-' ? 'Select a group on the left, or create one.' : `There is no group "${slug}" in this draft's tree.` }}
            </p>
            <template v-else>
              <nav class="text-xs text-gray-600">
                <template v-for="(s, i) in breadcrumb" :key="s">
                  <span v-if="i > 0"> / </span>
                  <RouterLink :to="`/catalog/drafts/${draftId}/groups/${s}`" class="hover:underline">{{ groupTitle(s) }}</RouterLink>
                </template>
              </nav>
              <label class="block text-sm font-semibold">
                Title
                <input
                  v-model="title"
                  type="text"
                  class="mt-1 w-full rounded border border-gray-300 px-3 py-2 font-normal"
                  @input="groupDirty = true"
                />
              </label>
              <h2 class="font-semibold">Children</h2>
              <ReorderList :items="children" :item-key="childKey" :disabled="saving" @update:items="onChildrenReordered">
                <template #item="{ item }">
                  <div class="flex items-center justify-between gap-2 text-sm">
                    <span v-if="item.group !== undefined">
                      Group:
                      <RouterLink :to="`/catalog/drafts/${draftId}/groups/${item.group}`" class="text-blue-700 hover:underline">
                        {{ groupTitle(item.group) }}
                      </RouterLink>
                    </span>
                    <span v-else>
                      Task:
                      <RouterLink :to="`/catalog/drafts/${draftId}/tasks/${item.task}`" class="text-blue-700 hover:underline">
                        {{ taskLabel.get(item.task ?? '') ?? item.task }}
                      </RouterLink>
                    </span>
                    <button type="button" class="text-xs text-red-700 hover:underline" :disabled="saving" @click="removeChild(item.key)">
                      Remove
                    </button>
                  </div>
                </template>
              </ReorderList>
              <p v-if="children.length === 0" class="text-sm text-gray-600">This group has no children.</p>
              <div class="flex gap-2">
                <select v-model="addTaskId" class="flex-1 rounded border border-gray-300 px-2 py-1 text-sm">
                  <option value="">Add a task that is in no group…</option>
                  <option v-for="t in ungroupedTasks" :key="t.id" :value="t.id">{{ t.title }} ({{ t.version }})</option>
                </select>
                <button
                  type="button"
                  class="rounded px-3 py-1 text-sm text-blue-700 hover:bg-gray-100 disabled:opacity-50"
                  :disabled="saving || addTaskId === ''"
                  @click="addTask"
                >
                  Add
                </button>
              </div>
              <div class="flex gap-2">
                <button
                  type="button"
                  class="rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-50"
                  :disabled="saving || !groupDirty || title.trim() === ''"
                  @click="saveGroup"
                >
                  Save group
                </button>
                <button
                  type="button"
                  class="rounded px-3 py-1 text-gray-700 hover:bg-gray-100 disabled:opacity-50"
                  :disabled="saving || !groupDirty"
                  @click="resetGroup"
                >
                  Discard
                </button>
              </div>
            </template>
          </section>
        </div>

        <section>
          <h2 class="mb-2 font-semibold">Problems in this draft</h2>
          <ProblemList v-if="detail.problems.length > 0" :problems="detail.problems" />
          <p v-else class="text-sm text-green-800">None.</p>
        </section>
      </template>
    </template>
  </section>
</template>
