<script setup lang="ts">
import { reactive } from 'vue'
import type { Node, NodeInput } from '../api'
import MarkdownField from './MarkdownField.vue'

const props = defineProps<{
  initial?: Node
  parentId?: string
  // askVersion shows the version field; it is required for new versions.
  askVersion?: boolean
  submitLabel: string
  busy?: boolean
}>()
const emit = defineEmits<{ submit: [NodeInput]; cancel: [] }>()

const form = reactive({
  displayName: props.initial?.displayName ?? '',
  description: props.initial?.description ?? '',
  predicateType: props.initial?.requiredAttestation.predicateType ?? '',
  subjectName: props.initial?.requiredAttestation.subjectName ?? '',
  identities: (props.initial?.requiredAttestation.requiredIdentities ?? []).join('\n'),
  missingReason: props.initial?.missingReason ?? '',
  version: '',
})

function submit() {
  emit('submit', {
    displayName: form.displayName.trim(),
    description: form.description,
    parentId: props.initial ? props.initial.parentId : props.parentId,
    requiredAttestation: {
      predicateType: form.predicateType.trim(),
      subjectName: form.subjectName.trim() || undefined,
      requiredIdentities: form.identities
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean),
    },
    missingReason: form.missingReason,
    version: form.version.trim() || undefined,
  })
}
</script>

<template>
  <form class="space-y-4" @submit.prevent="submit">
    <div class="grid gap-4 sm:grid-cols-3">
      <label class="block sm:col-span-2">
        <span class="label">Display name</span>
        <input v-model="form.displayName" class="input" required />
      </label>
      <label v-if="askVersion" class="block">
        <span class="label">New version</span>
        <input v-model="form.version" class="input" required :placeholder="initial ? `after ${initial.version}` : '1'" />
      </label>
    </div>
    <MarkdownField v-model="form.description" label="Description" :rows="5" />
    <fieldset class="space-y-3 rounded-md border border-stone-200 p-3">
      <legend class="px-1 text-xs font-semibold text-stone-500 uppercase">Required in-toto attestation</legend>
      <label class="block">
        <span class="label">Predicate type</span>
        <input v-model="form.predicateType" class="input font-mono" placeholder="https://slsa.dev/provenance/v1" />
      </label>
      <label class="block">
        <span class="label">Subject name (optional)</span>
        <input v-model="form.subjectName" class="input font-mono" />
      </label>
      <label class="block">
        <span class="label">Required signer identities (one per line, optional)</span>
        <textarea
          v-model="form.identities"
          class="input font-mono"
          rows="2"
          placeholder="key::ed25519::…  or  sigstore::https://token.actions.githubusercontent.com::…"
        />
      </label>
    </fieldset>
    <MarkdownField v-model="form.missingReason" label="Why the attestation is missing" :rows="3" />
    <div class="flex gap-2">
      <button class="btn btn-primary" :disabled="busy">{{ submitLabel }}</button>
      <button type="button" class="btn" @click="emit('cancel')">Cancel</button>
    </div>
  </form>
</template>
