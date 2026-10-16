<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../../api/client'
import { getToken } from '../../api/token'
import type { ErrorBody } from '../../types/error'
import type { Workspace } from '../../types/workspace'

const route = useRoute()
const workspaceId = computed(() => route.params.id as string)

const frozen = ref(false)
const warning = ref('')
const errorMessage = ref('')
const toggling = ref(false)
const downloading = ref(false)
const bookHtml = ref('')

// fetchBook loads the book directly with fetch (not api/client.ts's JSON
// client: this endpoint answers text/markdown or text/html, not JSON), but
// still attaches the same credentials ('include', the ambient session
// cookie in OIDC mode) and, in token-only mode, the same Authorization:
// Bearer header api/client.ts's request() attaches (api/token.ts's
// getToken()). A plain <a href> or <iframe src> pointed straight at the
// API cannot carry that header at all, so in token-only mode it would 401
// instead of showing the book — the same problem plan 4d's getRunLog hit
// for its own text/plain endpoint, fixed there with this identical
// pattern (found in code review).
async function fetchBook(format: 'markdown' | 'html'): Promise<string> {
  const token = getToken()
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = `Bearer ${token}`
  const res = await fetch(`/api/workspaces/${workspaceId.value}/book?format=${format}`, {
    credentials: 'include',
    headers,
  })
  const text = await res.text()
  if (!res.ok) {
    let msg = text
    try {
      const body = JSON.parse(text) as ErrorBody
      if (body && typeof body.error === 'string') msg = body.error
    } catch {
      // not JSON: keep the raw text
    }
    throw new Error(`Loading the book failed (HTTP ${res.status}): ${msg}`)
  }
  return text
}

async function loadPreview() {
  try {
    bookHtml.value = await fetchBook('html')
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  }
}

async function load() {
  try {
    const list = await api.get<Workspace[]>('/api/workspaces')
    const w = list.find((x) => x.id === workspaceId.value)
    frozen.value = w?.frozen ?? false
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  }
  await loadPreview()
}

onMounted(load)

// downloadMarkdown fetches the Markdown export the same authenticated way
// as the HTML preview, then triggers a save through a synthetic, invisible
// <a> with a blob: URL — a plain <a href="/api/..." download> cannot carry
// the Authorization header token-only mode needs (see fetchBook).
async function downloadMarkdown() {
  downloading.value = true
  errorMessage.value = ''
  try {
    const text = await fetchBook('markdown')
    const blob = new Blob([text], { type: 'text/markdown' })
    const url = URL.createObjectURL(blob)
    try {
      const a = document.createElement('a')
      a.href = url
      a.download = `${workspaceId.value}.md`
      document.body.appendChild(a)
      a.click()
      a.remove()
    } finally {
      URL.revokeObjectURL(url)
    }
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  } finally {
    downloading.value = false
  }
}

async function toggleFreeze() {
  toggling.value = true
  warning.value = ''
  errorMessage.value = ''
  try {
    const path = frozen.value ? 'unfreeze' : 'freeze'
    const res = await api.post<{ frozen: boolean; warning?: string }>(`/api/workspaces/${workspaceId.value}/${path}`)
    frozen.value = res.frozen
    if (res.warning) warning.value = res.warning
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  } finally {
    toggling.value = false
  }
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h1 class="text-xl font-semibold">Book</h1>
      <div class="flex items-center gap-3">
        <button
          class="text-sm text-gray-600 hover:text-gray-900 disabled:opacity-50"
          :disabled="downloading"
          @click="downloadMarkdown"
        >
          Download Markdown
        </button>
        <button
          class="rounded border px-3 py-1.5 text-sm font-medium disabled:opacity-50"
          :disabled="toggling"
          @click="toggleFreeze"
        >
          {{ frozen ? 'Unfreeze' : 'Freeze' }}
        </button>
      </div>
    </div>
    <p v-if="warning" class="text-sm text-amber-700">{{ warning }}</p>
    <p v-if="errorMessage" class="text-sm text-red-600">{{ errorMessage }}</p>
    <iframe :srcdoc="bookHtml" class="h-[80vh] w-full rounded border bg-white" title="Workspace book" />
  </div>
</template>
