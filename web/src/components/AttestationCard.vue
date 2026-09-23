<script setup lang="ts">
import { ref } from 'vue'
import { api, formatDate, type Attestation } from '../api'
import { useAction } from '../composables'
import Badge from './Badge.vue'
import ErrorMessage from './ErrorMessage.vue'
import VerificationBadge from './VerificationBadge.vue'

const props = defineProps<{ attestation: Attestation; nodeName?: string; stale?: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const action = useAction()
const raw = ref<string>()

async function reverify() {
  if (await action.run(() => api.reverify(props.attestation.id))) emit('changed')
}

async function remove() {
  if (!confirm('Delete this attestation?')) return
  await action.run(() => api.deleteAttestation(props.attestation.id))
  if (!action.error.value) emit('changed')
}

async function toggleRaw() {
  if (raw.value) {
    raw.value = undefined
    return
  }
  const a = await action.run(() => api.attestation(props.attestation.id))
  if (a) raw.value = JSON.stringify(a.raw, null, 2)
}
</script>

<template>
  <div class="rounded-md border border-stone-200 p-3 text-sm">
    <div class="flex flex-wrap items-center gap-2">
      <VerificationBadge :status="attestation.verification.status" :met="attestation.verification.requirementMet" />
      <Badge v-if="stale" tone="warn">for an older version</Badge>
      <Badge>{{ attestation.format }}</Badge>
      <span v-if="nodeName" class="font-medium">{{ nodeName }}</span>
      <span class="ml-auto text-xs text-stone-400">{{ formatDate(attestation.createdAt) }}</span>
    </div>
    <dl class="mt-2 space-y-1">
      <div class="flex gap-2">
        <dt class="w-20 shrink-0 text-stone-500">Predicate</dt>
        <dd class="font-mono break-all">{{ attestation.predicateType }}</dd>
      </div>
      <div v-if="attestation.subjects?.length" class="flex gap-2">
        <dt class="w-20 shrink-0 text-stone-500">Subjects</dt>
        <dd class="font-mono break-all">{{ attestation.subjects.map((s) => s.name || s.uri || '?').join(', ') }}</dd>
      </div>
      <div v-if="attestation.verification.identities?.length" class="flex gap-2">
        <dt class="w-20 shrink-0 text-stone-500">Signed by</dt>
        <dd>
          <p v-for="i in attestation.verification.identities" :key="i" class="font-mono text-xs break-all">{{ i }}</p>
        </dd>
      </div>
    </dl>
    <p v-if="attestation.verification.error" class="mt-2 text-rose-700">{{ attestation.verification.error }}</p>
    <ul v-if="attestation.verification.requirementErrors?.length" class="mt-2 list-inside list-disc text-amber-800">
      <li v-for="e in attestation.verification.requirementErrors" :key="e">{{ e }}</li>
    </ul>
    <div class="mt-3 flex gap-2">
      <button class="btn" :disabled="action.busy.value" @click="reverify">Verify again</button>
      <button class="btn" @click="toggleRaw">{{ raw ? 'Hide' : 'Show' }} JSON</button>
      <button class="btn btn-danger" @click="remove">Delete</button>
    </div>
    <ErrorMessage :error="action.error.value" class="mt-2" />
    <pre v-if="raw" class="mt-2 max-h-96 overflow-auto rounded bg-stone-900 p-3 text-xs text-stone-100">{{ raw }}</pre>
  </div>
</template>
