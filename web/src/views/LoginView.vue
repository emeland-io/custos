<script setup lang="ts">
import { ref } from 'vue'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()
const token = ref('')
const error = ref('')
const submitting = ref(false)

async function submitToken() {
  error.value = ''
  submitting.value = true
  try {
    await auth.loginWithToken(token.value.trim())
    window.location.href = '/'
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'sign-in failed'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="mx-auto mt-16 max-w-sm space-y-6 rounded-lg border bg-white p-6 shadow-sm">
    <h1 class="text-lg font-semibold">Sign in to custos</h1>

    <div v-if="auth.mode === 'token-only'" class="space-y-3">
      <p class="text-sm text-gray-600">
        This server has no identity provider configured (token-only mode). Paste a personal access token
        created with <code>custos token create</code>.
      </p>
      <input
        v-model="token"
        type="password"
        placeholder="personal access token"
        class="w-full rounded border px-3 py-2 text-sm"
        @keyup.enter="submitToken"
      />
      <button
        class="w-full rounded bg-gray-900 px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
        :disabled="!token || submitting"
        @click="submitToken"
      >
        Sign in with token
      </button>
      <p v-if="error" class="text-sm text-red-600">{{ error }}</p>
    </div>

    <div v-else class="space-y-3">
      <p class="text-sm text-gray-600">Sign in with your organization's identity provider.</p>
      <button class="w-full rounded bg-gray-900 px-3 py-2 text-sm font-medium text-white" @click="auth.login()">
        Sign in
      </button>
    </div>
  </div>
</template>
