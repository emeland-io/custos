<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import ProblemList from '../../components/ProblemList.vue'
import MergeToolView from './MergeToolView.vue'
import {
  conflictsOf,
  createDraft,
  deleteDraft,
  errorMessage,
  getDraft,
  listDrafts,
  problemsOf,
  publishDraft,
  statusOf,
} from '../../api/authors'
import { routeParam, useAuthorRole } from '../../router/authors'
import type { CatalogProblem, DecodedConflict, Draft, DraftDetail, ResolutionJSON } from '../../types/taskAuthor'

type PublishState = 'idle' | 'publishing' | 'conflicts' | 'failed'

const route = useRoute()
const router = useRouter()
const allowed = useAuthorRole('task_author')

const drafts = ref<Draft[]>([])
const listError = ref('')
const newTitle = ref('')
const creating = ref(false)

const draftId = computed(() => routeParam(route.params.draftId))
const publishedCommit = computed(() => routeParam(route.query.published))
const detail = ref<DraftDetail | null>(null)
const detailError = ref('')
const loadingDetail = ref(false)

const publishState = ref<PublishState>('idle')
const publishError = ref('')
const publishProblems = ref<CatalogProblem[]>([])
const conflicts = ref<DecodedConflict[]>([])
const sentResolutions = ref<Record<string, ResolutionJSON>>({})

const tasks = computed(() => [...(detail.value?.catalog.tasks ?? [])].sort((a, b) => a.title.localeCompare(b.title)))
// "-" is never a group slug (slugs start with [a-z0-9]); GroupTreeView reads it as "no group selected".
const firstGroupSlug = computed(() => detail.value?.catalog.groups[0]?.slug ?? '-')

function resetPublish() {
  publishState.value = 'idle'
  publishError.value = ''
  publishProblems.value = []
  conflicts.value = []
  sentResolutions.value = {}
}

async function loadList() {
  listError.value = ''
  try {
    drafts.value = await listDrafts()
  } catch (e) {
    listError.value = errorMessage(e)
  }
}

async function loadDetail() {
  resetPublish()
  detail.value = null
  detailError.value = ''
  if (draftId.value === '') return
  loadingDetail.value = true
  try {
    detail.value = await getDraft(draftId.value)
  } catch (e) {
    detailError.value =
      statusOf(e) === 404 ? 'This draft does not exist; it may have been published or discarded.' : errorMessage(e)
  } finally {
    loadingDetail.value = false
  }
}

async function create() {
  const title = newTitle.value.trim()
  if (title === '' || creating.value) return
  creating.value = true
  listError.value = ''
  try {
    const d = await createDraft(title)
    newTitle.value = ''
    await loadList()
    await router.push({ name: 'catalog-draft', params: { draftId: d.id } })
  } catch (e) {
    listError.value = errorMessage(e)
  } finally {
    creating.value = false
  }
}

async function discard(d: Draft) {
  if (!window.confirm(`Discard the draft "${d.title}"? Its unpublished changes are lost.`)) return
  listError.value = ''
  try {
    await deleteDraft(d.id)
  } catch (e) {
    listError.value = errorMessage(e)
    return
  }
  await loadList()
  if (draftId.value === d.id) await router.push({ name: 'catalog-drafts' })
}

/** Publishes the open draft, re-sending earlier resolutions together with extra. */
async function publish(extra: Record<string, ResolutionJSON> = {}) {
  const d = detail.value
  if (d === null || publishState.value === 'publishing') return
  const resolutions = { ...sentResolutions.value, ...extra }
  publishState.value = 'publishing'
  publishError.value = ''
  publishProblems.value = []
  try {
    const res = await publishDraft(d.draft.id, resolutions)
    resetPublish()
    await loadList()
    await router.push({ name: 'catalog-drafts', query: { published: res.commit } })
  } catch (e) {
    const cs = conflictsOf(e)
    if (cs !== null) {
      sentResolutions.value = resolutions
      conflicts.value = cs
      publishState.value = 'conflicts'
      return
    }
    const hadResolutions = Object.keys(resolutions).length > 0
    sentResolutions.value = {}
    conflicts.value = []
    publishState.value = 'failed'
    publishProblems.value = problemsOf(e)
    publishError.value = errorMessage(e)
    if (hadResolutions && statusOf(e) === 400) {
      publishError.value +=
        ' The published catalog changed again while you were resolving, so your choices were discarded. Press Publish to start over.'
    } else if (statusOf(e) === 409) {
      publishError.value += ' Press Publish to try again.'
    }
  }
}

watch(allowed, (ok) => {
  if (ok) void loadList()
}, { immediate: true })

watch([allowed, draftId], () => {
  if (allowed.value) void loadDetail()
}, { immediate: true })
</script>

<template>
  <section class="mx-auto max-w-5xl space-y-8 p-6">
    <p v-if="!allowed" class="text-gray-700">Catalog drafts need the task-author role.</p>
    <template v-else>
      <header class="space-y-1">
        <h1 class="text-2xl font-semibold">Catalog drafts</h1>
        <p class="text-sm text-gray-600">
          Changes to tasks and groups are made in a draft. Publishing validates the draft and merges it into the
          catalog's main.
        </p>
      </header>

      <p v-if="publishedCommit !== ''" class="rounded bg-green-50 p-3 text-green-900">
        Published as catalog commit <code>{{ publishedCommit.slice(0, 12) }}</code>.
      </p>

      <form class="flex gap-2" @submit.prevent="create">
        <input
          v-model="newTitle"
          type="text"
          placeholder="Title of a new draft"
          class="flex-1 rounded border border-gray-300 px-3 py-2"
          :disabled="creating"
        />
        <button
          type="submit"
          class="rounded bg-blue-600 px-4 py-2 text-white disabled:opacity-50"
          :disabled="creating || newTitle.trim() === ''"
        >
          Create draft
        </button>
      </form>
      <p v-if="listError" class="text-red-700">{{ listError }}</p>

      <table v-if="drafts.length > 0" class="w-full text-left text-sm">
        <thead>
          <tr class="border-b">
            <th class="py-2">Title</th>
            <th>Author</th>
            <th>Based on</th>
            <th>Head</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in drafts" :key="d.id" class="border-b" :class="{ 'bg-blue-50': d.id === draftId }">
            <td class="py-2">
              <RouterLink :to="`/catalog/drafts/${d.id}`" class="text-blue-700 hover:underline">{{ d.title }}</RouterLink>
            </td>
            <td>{{ d.author.name }}</td>
            <td><code>{{ d.base.slice(0, 8) }}</code></td>
            <td><code>{{ d.commit.slice(0, 8) }}</code></td>
            <td class="text-right">
              <button type="button" class="text-red-700 hover:underline" @click="discard(d)">Discard</button>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-else-if="listError === ''" class="text-gray-600">No open drafts.</p>

      <section v-if="draftId !== ''" class="space-y-6 border-t pt-6">
        <p v-if="loadingDetail" class="text-gray-600">Loading the draft…</p>
        <p v-else-if="detailError" class="text-red-700">{{ detailError }}</p>
        <template v-else-if="detail">
          <header class="flex flex-wrap items-baseline justify-between gap-2">
            <h2 class="text-xl font-semibold">{{ detail.draft.title }}</h2>
            <span class="text-sm text-gray-600">
              branch <code>{{ detail.draft.branch }}</code> by {{ detail.draft.author.name }}
            </span>
          </header>

          <nav class="text-sm">
            <RouterLink
              :to="`/catalog/drafts/${detail.draft.id}/groups/${firstGroupSlug}`"
              class="text-blue-700 hover:underline"
            >
              Edit the group tree
            </RouterLink>
          </nav>

          <div>
            <h3 class="mb-2 font-semibold">Problems</h3>
            <ProblemList v-if="detail.problems.length > 0" :problems="detail.problems" />
            <p v-else class="text-sm text-green-800">None: this draft passes validation as it stands.</p>
          </div>

          <div>
            <h3 class="mb-2 font-semibold">Tasks</h3>
            <table class="w-full text-left text-sm">
              <thead>
                <tr class="border-b">
                  <th class="py-2">Title</th>
                  <th>Current version</th>
                  <th>Answer type</th>
                  <th>Processor</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="t in tasks" :key="t.id" class="border-b">
                  <td class="py-2">{{ t.title }}</td>
                  <td>{{ t.version }}</td>
                  <td>{{ t.answer_type }}</td>
                  <td>{{ t.processor ?? '' }}</td>
                  <td class="text-right">
                    <RouterLink
                      :to="`/catalog/drafts/${detail.draft.id}/tasks/${t.id}`"
                      class="text-blue-700 hover:underline"
                    >
                      New version…
                    </RouterLink>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <div class="space-y-3">
            <button
              type="button"
              class="rounded bg-green-700 px-4 py-2 text-white disabled:opacity-50"
              :disabled="publishState === 'publishing' || conflicts.length > 0"
              @click="publish()"
            >
              {{ publishState === 'publishing' && conflicts.length === 0 ? 'Publishing…' : 'Publish' }}
            </button>
            <p v-if="detail.problems.length > 0" class="text-sm text-amber-800">
              This draft has problems; publishing is rejected until they are fixed.
            </p>
            <p v-if="publishError" class="text-red-700">{{ publishError }}</p>
            <ProblemList v-if="publishProblems.length > 0" :problems="publishProblems" />
            <MergeToolView
              v-if="conflicts.length > 0"
              :conflicts="conflicts"
              :busy="publishState === 'publishing'"
              @resolve="publish"
              @cancel="resetPublish"
            />
          </div>
        </template>
      </section>
    </template>
  </section>
</template>
