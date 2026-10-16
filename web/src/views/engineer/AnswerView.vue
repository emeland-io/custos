<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, ApiError } from '../../api/client'
import ProblemList from '../../components/ProblemList.vue'
import MarkdownView from '../../components/MarkdownView.vue'
import { diffLines } from '../../lib/diff'
import type { Status, TaskStatus, Attachment } from '../../types/status'
import type { TaskHistory, TaskVersion } from '../../types/catalog'
import type { Problem } from '../../types/error'

const route = useRoute()
const router = useRouter()

const workspaceId = computed(() => route.params.id as string)
const taskId = computed(() => route.params.taskId as string)

const task = ref<TaskStatus | null>(null)
const history = ref<TaskHistory | null>(null)
// historyUnavailable is also true for a generated task: there is still no
// endpoint exposing a generated task's body (title/answer_type/choices are
// now carried by GET /api/workspaces/{id}/status itself — plan 4b, Task 8
// — which is enough to render and answer a choice-type generated task; the
// task's descriptive body text remains catalog-tasks-only).
const historyUnavailable = ref(false)

const value = ref('')
const body = ref('')
const attachments = ref<Attachment[]>([])
const uploading = ref(false)
const saving = ref(false)
const confirming = ref(false)
const commit = ref('')
const errorMessage = ref('')
const problems = ref<Problem[]>([])

async function load() {
  errorMessage.value = ''
  const status = await api.get<Status>(`/api/workspaces/${workspaceId.value}/status`)
  const found = status.tasks.find((t) => t.id === taskId.value)
  if (!found) {
    errorMessage.value = `workspace ${workspaceId.value} has no task ${taskId.value}`
    return
  }
  task.value = found
  value.value = found.answer?.value ?? ''
  body.value = found.answer?.body ?? ''
  attachments.value = found.answer?.attachments ?? []

  if (found.generated) {
    historyUnavailable.value = true
    return
  }
  try {
    history.value = await api.get<TaskHistory>(`/api/catalog/tasks/${taskId.value}`)
  } catch {
    historyUnavailable.value = true
  }
}

onMounted(load)

const currentVersion = computed<TaskVersion | undefined>(() =>
  history.value?.versions.find((v) => v.version === history.value!.current),
)
const answeredVersion = computed<TaskVersion | undefined>(() =>
  history.value?.versions.find((v) => v.version === task.value?.answer?.task_version),
)
// currentVersion.choices covers a catalog task; task.choices (status.ts,
// plan 4b Task 8) covers a generated task, which has no TaskHistory.
const choices = computed(() => currentVersion.value?.choices ?? task.value?.choices ?? [])

const showDiff = computed(
  () => !!currentVersion.value && !!answeredVersion.value && currentVersion.value.version !== answeredVersion.value.version,
)
const bodyDiff = computed(() => (showDiff.value ? diffLines(answeredVersion.value!.body, currentVersion.value!.body) : []))

async function onFileChosen(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  uploading.value = true
  errorMessage.value = ''
  try {
    const { sha256 } = await api.putBlob(file)
    attachments.value.push({ name: file.name, sha256, media_type: file.type || 'application/octet-stream', available: true })
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  } finally {
    uploading.value = false
    input.value = ''
  }
}

function removeAttachment(i: number) {
  attachments.value.splice(i, 1)
}

async function save() {
  if (!task.value) return
  saving.value = true
  errorMessage.value = ''
  problems.value = []
  try {
    const res = await api.put<{ commit: string }>(`/api/workspaces/${workspaceId.value}/answers/${taskId.value}`, {
      value: task.value.answer_type === 'markdown' ? '' : value.value,
      body: task.value.answer_type === 'markdown' ? body.value : '',
      attachments: attachments.value.map((a) => ({ name: a.name, sha256: a.sha256, media_type: a.media_type })),
    })
    commit.value = res.commit
    await load()
  } catch (err) {
    handleError(err)
  } finally {
    saving.value = false
  }
}

async function confirmStillValid() {
  confirming.value = true
  errorMessage.value = ''
  problems.value = []
  try {
    const res = await api.post<{ commit: string }>(`/api/workspaces/${workspaceId.value}/answers/${taskId.value}/still-valid`)
    commit.value = res.commit
    await load()
  } catch (err) {
    handleError(err)
  } finally {
    confirming.value = false
  }
}

function handleError(err: unknown) {
  if (err instanceof ApiError) {
    errorMessage.value = err.body.error
    problems.value = err.body.problems ?? []
  } else {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  }
}

function goBack() {
  router.push(`/w/${workspaceId.value}`)
}
</script>

<template>
  <div v-if="task" class="space-y-6">
    <div>
      <h1 class="text-xl font-semibold">{{ task.title }}</h1>
      <p class="text-sm text-gray-500">
        {{ task.generated ? 'generated task' : 'catalog task' }} · state: {{ task.state }} · answer type:
        {{ task.answer_type }}
      </p>
    </div>

    <section v-if="currentVersion" class="rounded border bg-white p-4">
      <h2 class="mb-2 font-medium text-gray-700">Task description (version {{ currentVersion.version }})</h2>
      <MarkdownView :source="currentVersion.body" />
    </section>
    <p v-else-if="historyUnavailable" class="text-sm text-gray-500">
      No task description is available for a generated task yet (see this plan's open question about a missing
      generated-task detail endpoint).
    </p>

    <section v-if="showDiff" class="rounded border bg-white p-4">
      <h2 class="mb-2 font-medium text-gray-700">What changed since you answered version {{ answeredVersion!.version }}</h2>
      <pre class="overflow-x-auto rounded bg-gray-50 p-3 text-xs"><span
        v-for="(l, i) in bodyDiff"
        :key="i"
        :class="{ 'bg-green-100': l.kind === 'added', 'bg-red-100 line-through': l.kind === 'removed' }"
      >{{ l.text }}<br /></span></pre>
      <button
        class="mt-3 rounded bg-gray-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
        :disabled="confirming"
        @click="confirmStillValid"
      >
        Still valid — keep this answer
      </button>
    </section>

    <section class="space-y-3 rounded border bg-white p-4">
      <h2 class="font-medium text-gray-700">Your answer</h2>

      <textarea
        v-if="task.answer_type === 'markdown'"
        v-model="body"
        rows="10"
        class="w-full rounded border px-3 py-2 font-mono text-sm"
        placeholder="Write the answer in Markdown"
      />
      <input
        v-else-if="task.answer_type === 'text'"
        v-model="value"
        type="text"
        class="w-full rounded border px-3 py-2 text-sm"
      />
      <div v-else-if="task.answer_type === 'timestamp'" class="flex gap-2">
        <input
          v-model="value"
          type="text"
          placeholder="2026-10-09T12:00:00Z"
          class="w-full rounded border px-3 py-2 font-mono text-sm"
        />
        <button type="button" class="rounded border px-3 py-2 text-sm" @click="value = new Date().toISOString()">
          now
        </button>
      </div>
      <input
        v-else-if="task.answer_type === 'path'"
        v-model="value"
        type="text"
        placeholder="/path/to/file"
        class="w-full rounded border px-3 py-2 text-sm"
      />
      <input
        v-else-if="task.answer_type === 'url'"
        v-model="value"
        type="url"
        class="w-full rounded border px-3 py-2 text-sm"
      />
      <template v-else-if="task.answer_type === 'choice'">
        <select v-model="value" class="w-full rounded border px-3 py-2 text-sm">
          <option value="" disabled>choose one</option>
          <option v-for="c in choices" :key="c" :value="c">{{ c }}</option>
        </select>
        <p v-if="!choices.length" class="text-sm text-amber-700">
          No choices are recorded for this task. Saving is disabled.
        </p>
      </template>

      <div>
        <h3 class="mb-1 text-sm font-medium text-gray-600">Attachments</h3>
        <ul class="mb-2 space-y-1">
          <li v-for="(a, i) in attachments" :key="a.sha256" class="flex items-center justify-between text-sm">
            <span>
              {{ a.name }}
              <span class="font-mono text-xs text-gray-400">{{ a.sha256.slice(0, 12) }}…</span>
              <span v-if="!a.available" class="ml-1 text-xs text-red-600">attachment unavailable</span>
            </span>
            <button class="text-xs text-gray-500 hover:text-red-600" @click="removeAttachment(i)">remove</button>
          </li>
        </ul>
        <input type="file" :disabled="uploading" @change="onFileChosen" />
      </div>

      <ProblemList :problems="problems" />
      <p v-if="errorMessage" class="text-sm text-red-600">{{ errorMessage }}</p>
      <p v-if="commit" class="text-sm text-green-700">Saved as commit {{ commit.slice(0, 12) }}…</p>

      <div>
        <button
          class="rounded bg-gray-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
          :disabled="saving || (task.answer_type === 'choice' && !choices.length)"
          @click="save"
        >
          Save answer
        </button>
        <button class="ml-2 text-sm text-gray-600 hover:text-gray-900" @click="goBack">Back to dashboard</button>
      </div>
    </section>
  </div>
  <p v-else-if="errorMessage" class="text-sm text-red-600">{{ errorMessage }}</p>
</template>
