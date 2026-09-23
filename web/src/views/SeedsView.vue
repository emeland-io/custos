<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api, formatDate } from '../api'
import { useAction, useLoad } from '../composables'
import ErrorMessage from '../components/ErrorMessage.vue'
import MarkdownField from '../components/MarkdownField.vue'

const router = useRouter()
const seeds = useLoad(api.seeds)
const roots = useLoad(api.roots)
const rootNames = computed(() => new Map((roots.data.value ?? []).map((r) => [r.id, r.displayName])))
const creating = ref(false)
const form = reactive({ displayName: '', description: '', rootId: '' })
const action = useAction()

async function create() {
  const seed = await action.run(() => api.createSeed(form))
  if (seed) void router.push(`/seeds/${seed.id}`)
}
</script>

<template>
  <div class="mb-6 flex items-center justify-between">
    <h1 class="text-2xl font-bold tracking-tight">Seeds</h1>
    <button v-if="!creating" class="btn btn-primary" :disabled="!roots.data.value?.length" @click="creating = true">
      New seed
    </button>
  </div>
  <ErrorMessage :error="seeds.error.value" />

  <form v-if="creating" class="card mb-6 space-y-4" @submit.prevent="create">
    <div class="grid gap-4 sm:grid-cols-2">
      <label class="block">
        <span class="label">Display name</span>
        <input v-model="form.displayName" class="input" required />
      </label>
      <label class="block">
        <span class="label">Root</span>
        <select v-model="form.rootId" class="input" required>
          <option value="" disabled>Choose a root</option>
          <option v-for="r in roots.data.value" :key="r.id" :value="r.id">{{ r.displayName }}</option>
        </select>
      </label>
    </div>
    <MarkdownField v-model="form.description" label="Description" :rows="3" />
    <ErrorMessage :error="action.error.value" />
    <div class="flex gap-2">
      <button class="btn btn-primary" :disabled="action.busy.value">Create</button>
      <button type="button" class="btn" @click="creating = false">Cancel</button>
    </div>
  </form>

  <p v-if="seeds.data.value && !seeds.data.value.length" class="text-stone-500">
    No seeds yet. A seed is one run through the tree of a root, collecting answers and attestations.
  </p>
  <ul class="grid gap-3">
    <li v-for="s in seeds.data.value" :key="s.id">
      <RouterLink :to="`/seeds/${s.id}`" class="card flex items-center justify-between hover:border-emerald-600">
        <div>
          <h2 class="font-semibold">{{ s.displayName }}</h2>
          <p class="text-sm text-stone-500">{{ rootNames.get(s.rootId) ?? 'unknown root' }}</p>
        </div>
        <span class="text-xs text-stone-400">{{ formatDate(s.createdAt) }}</span>
      </RouterLink>
    </li>
  </ul>
</template>
