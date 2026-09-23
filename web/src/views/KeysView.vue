<script setup lang="ts">
import { api } from '../api'
import { useAction, useLoad } from '../composables'
import ErrorMessage from '../components/ErrorMessage.vue'

const keys = useLoad(api.keys)
const action = useAction()

async function reload() {
  const k = await action.run(api.reloadKeys)
  if (k) keys.data.value = k
}

function copy(text: string) {
  void navigator.clipboard?.writeText(text)
}
</script>

<template>
  <div class="mb-6 flex items-center justify-between">
    <h1 class="text-2xl font-bold tracking-tight">Trusted keys</h1>
    <button class="btn" :disabled="action.busy.value" @click="reload">Reload from disk</button>
  </div>
  <p class="mb-4 text-sm text-stone-600">
    Signatures made with these public keys are verified. Put PEM or GPG public keys into the keys directory and reload.
    A node can require a signer by listing the key's identity. Sigstore signers are required as
    <code class="rounded bg-stone-100 px-1">sigstore::&lt;issuer&gt;::&lt;identity&gt;</code>; matchers such as
    <code class="rounded bg-stone-100 px-1">sigstore(identityMatch=prefix)::…</code> are supported.
  </p>
  <ErrorMessage :error="keys.error.value ?? action.error.value" />
  <p v-if="keys.data.value && !keys.data.value.length" class="text-stone-500">No keys loaded.</p>
  <ul class="space-y-2">
    <li v-for="k in keys.data.value" :key="k.identity" class="card flex items-center justify-between gap-4">
      <div class="min-w-0">
        <p class="font-medium">{{ k.file }}</p>
        <p class="font-mono text-xs break-all text-stone-500">{{ k.identity }}</p>
      </div>
      <button class="btn shrink-0" @click="copy(k.identity)">Copy identity</button>
    </li>
  </ul>
</template>
