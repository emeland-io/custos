<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, onBeforeRouteLeave, useRoute } from 'vue-router'
import MarkdownView from '../../components/MarkdownView.vue'
import ProblemList from '../../components/ProblemList.vue'
import LineDiff from './LineDiff.vue'
import VersionGraphView from './VersionGraphView.vue'
import { bumpVersion } from './semver'
import { errorMessage, getDraft, problemsOf, putTaskVersion, statusOf } from '../../api/authors'
import { routeParam, useAuthorRole } from '../../router/authors'
import { ANSWER_TYPES } from '../../types/taskAuthor'
import type { AnswerType, CatalogProblem, DraftDetail, NewVersionRequest, SemverStep } from '../../types/taskAuthor'

const STEPS: { value: SemverStep; label: string }[] = [
  { value: 'patch', label: 'Patch' },
  { value: 'minor', label: 'Minor' },
  { value: 'major', label: 'Major' },
]
const WIZARD = ['1. Version step', '2. Edit', '3. Review'] as const

const route = useRoute()
const allowed = useAuthorRole('task_author')
const draftId = computed(() => routeParam(route.params.draftId))
const taskId = computed(() => routeParam(route.params.taskId))

const detail = ref<DraftDetail | null>(null)
const loadError = ref('')
const current = computed(() => detail.value?.catalog.tasks.find((t) => t.id === taskId.value) ?? null)

const wizardStep = ref<1 | 2 | 3>(1)
const step = ref<SemverStep>('minor')
const title = ref('')
const answerType = ref<AnswerType>('markdown')
const choicesText = ref('')
const body = ref('')
const saving = ref(false)
const saveError = ref('')
const saveProblems = ref<CatalogProblem[]>([])
const notice = ref('')

const nextVersion = computed(() => (current.value ? bumpVersion(current.value.version, step.value) : ''))
const choices = computed(() =>
  choicesText.value
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s !== ''),
)

const formError = computed(() => {
  if (title.value.trim() === '') return 'The title must not be empty.'
  if (answerType.value === 'choice' && choices.value.length === 0) return 'A choice task needs at least one choice, one per line.'
  return ''
})

const metaChanges = computed(() => {
  const c = current.value
  if (!c) return []
  const out: string[] = []
  const t = title.value.trim()
  if (t !== c.title) out.push(`Title: "${c.title}" → "${t}"`)
  if (answerType.value !== c.answer_type) out.push(`Answer type: ${c.answer_type} → ${answerType.value}`)
  const before = (c.choices ?? []).join(', ')
  const after = answerType.value === 'choice' ? choices.value.join(', ') : ''
  if (before !== after) out.push(`Choices: ${before || '(none)'} → ${after || '(none)'}`)
  return out
})

const unchanged = computed(() => metaChanges.value.length === 0 && current.value !== null && body.value === current.value.body)

function resetForm() {
  const c = current.value
  wizardStep.value = 1
  step.value = 'minor'
  title.value = c?.title ?? ''
  answerType.value = c?.answer_type ?? 'markdown'
  choicesText.value = (c?.choices ?? []).join('\n')
  body.value = c?.body ?? ''
}

async function load() {
  loadError.value = ''
  saveError.value = ''
  saveProblems.value = []
  if (!allowed.value || draftId.value === '') return
  try {
    detail.value = await getDraft(draftId.value)
    resetForm()
  } catch (e) {
    detail.value = null
    loadError.value =
      statusOf(e) === 404 ? 'This draft does not exist; it may have been published or discarded.' : errorMessage(e)
  }
}

watch([allowed, draftId, taskId], () => {
  notice.value = ''
  void load()
}, { immediate: true })

async function save() {
  const c = current.value
  if (c === null || formError.value !== '' || saving.value) return
  const req: NewVersionRequest = {
    step: step.value,
    title: title.value.trim(),
    body: body.value,
    answer_type: answerType.value,
  }
  if (answerType.value === 'choice') req.choices = choices.value
  const made = nextVersion.value
  saving.value = true
  saveError.value = ''
  saveProblems.value = []
  try {
    await putTaskVersion(draftId.value, c.id, req)
    await load()
    notice.value = `Version ${made} is saved in this draft. Publish the draft to make it the current version in the catalog.`
  } catch (e) {
    saveError.value = errorMessage(e)
    saveProblems.value = problemsOf(e)
  } finally {
    saving.value = false
  }
}

onBeforeRouteLeave(() => wizardStep.value === 1 || saving.value || window.confirm('Discard this unsaved new version?'))
</script>

<template>
  <section class="mx-auto max-w-5xl space-y-6 p-6">
    <p v-if="!allowed" class="text-gray-700">Editing tasks needs the task-author role.</p>
    <template v-else>
      <nav class="text-sm">
        <RouterLink :to="`/catalog/drafts/${draftId}`" class="text-blue-700 hover:underline">← Back to the draft</RouterLink>
      </nav>
      <p v-if="loadError" class="text-red-700">{{ loadError }}</p>
      <template v-else-if="detail">
        <p v-if="!current" class="text-gray-700">
          Task <code>{{ taskId }}</code> has no current version in this draft: it is unknown, was merged into another
          task, or has two current versions that a new version must merge first.
        </p>
        <template v-else>
          <header class="space-y-1">
            <h1 class="text-2xl font-semibold">{{ current.title }}</h1>
            <p class="text-sm text-gray-600">
              Task <code>{{ current.id }}</code> · current version in this draft {{ current.version }} ·
              {{ current.answer_type }}
            </p>
          </header>
          <p v-if="notice" class="rounded bg-green-50 p-2 text-green-900">{{ notice }}</p>

          <VersionGraphView :task-id="current.id" :draft-version="current" />

          <ol class="flex gap-4 text-sm">
            <li v-for="(label, i) in WIZARD" :key="label" :class="wizardStep === i + 1 ? 'font-semibold text-blue-700' : 'text-gray-500'">
              {{ label }}
            </li>
          </ol>

          <section v-if="wizardStep === 1" class="space-y-3">
            <p class="text-sm">
              A new version copies version {{ current.version }}, lists it as its previous version, and increases the
              version number by the step you choose.
            </p>
            <fieldset class="space-y-2">
              <legend class="sr-only">Version step</legend>
              <label v-for="s in STEPS" :key="s.value" class="flex items-center gap-2">
                <input v-model="step" type="radio" name="step" :value="s.value" />
                <span class="w-12 font-medium">{{ s.label }}</span>
                <span class="font-mono text-sm text-gray-600">{{ current.version }} → {{ bumpVersion(current.version, s.value) }}</span>
              </label>
            </fieldset>
            <button type="button" class="rounded bg-blue-600 px-4 py-2 text-white" @click="wizardStep = 2">Next: edit</button>
          </section>

          <section v-else-if="wizardStep === 2" class="space-y-3">
            <label class="block text-sm font-semibold">
              Title
              <input v-model="title" type="text" class="mt-1 w-full rounded border border-gray-300 px-3 py-2 font-normal" />
            </label>
            <label class="block text-sm font-semibold">
              Answer type
              <select v-model="answerType" class="mt-1 block rounded border border-gray-300 px-3 py-2 font-normal">
                <option v-for="t in ANSWER_TYPES" :key="t" :value="t">{{ t }}</option>
              </select>
            </label>
            <label v-if="answerType === 'choice'" class="block text-sm font-semibold">
              Choices, one per line
              <textarea v-model="choicesText" rows="4" class="mt-1 w-full rounded border border-gray-300 p-2 font-mono font-normal" />
            </label>
            <label class="block text-sm font-semibold">
              Description (Markdown)
              <textarea v-model="body" rows="16" class="mt-1 w-full rounded border border-gray-300 p-2 font-mono font-normal" />
            </label>
            <p v-if="formError" class="text-sm text-red-700">{{ formError }}</p>
            <div class="flex gap-2">
              <button type="button" class="rounded px-4 py-2 text-gray-700 hover:bg-gray-100" @click="wizardStep = 1">Back</button>
              <button
                type="button"
                class="rounded bg-blue-600 px-4 py-2 text-white disabled:opacity-50"
                :disabled="formError !== ''"
                @click="wizardStep = 3"
              >
                Next: review
              </button>
            </div>
          </section>

          <section v-else class="space-y-4">
            <h2 class="text-lg font-semibold">Version {{ nextVersion }} (previous: {{ current.version }})</h2>
            <ul v-if="metaChanges.length > 0" class="list-disc pl-6 text-sm">
              <li v-for="m in metaChanges" :key="m">{{ m }}</li>
            </ul>
            <p v-else class="text-sm text-gray-600">Title, answer type and choices are unchanged.</p>
            <p v-if="unchanged" class="text-sm text-amber-800">Nothing changed: the new version would equal {{ current.version }}.</p>
            <h3 class="font-semibold">Description changes</h3>
            <LineDiff :before="current.body" :after="body" />
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <h3 class="mb-1 text-sm font-semibold">Current ({{ current.version }})</h3>
                <MarkdownView :source="current.body" />
              </div>
              <div>
                <h3 class="mb-1 text-sm font-semibold">New ({{ nextVersion }})</h3>
                <MarkdownView :source="body" />
              </div>
            </div>
            <p v-if="saveError" class="text-red-700">{{ saveError }}</p>
            <ProblemList v-if="saveProblems.length > 0" :problems="saveProblems" />
            <div class="flex gap-2">
              <button type="button" class="rounded px-4 py-2 text-gray-700 hover:bg-gray-100" :disabled="saving" @click="wizardStep = 2">
                Back
              </button>
              <button
                type="button"
                class="rounded bg-green-700 px-4 py-2 text-white disabled:opacity-50"
                :disabled="saving || formError !== ''"
                @click="save"
              >
                {{ saving ? 'Saving…' : `Save version ${nextVersion} in the draft` }}
              </button>
            </div>
          </section>
        </template>
      </template>
    </template>
  </section>
</template>
