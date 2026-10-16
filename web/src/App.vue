<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from './stores/auth'
import RoleGate from './components/RoleGate.vue'

const auth = useAuthStore()
const route = useRoute()

onMounted(() => {
  auth.ensureInit()
})

const workspaceId = computed(() => (typeof route.params.id === 'string' ? route.params.id : undefined))
</script>

<template>
  <div class="min-h-screen bg-gray-50 text-gray-900">
    <header class="border-b bg-white">
      <nav class="mx-auto flex max-w-5xl items-center gap-4 px-4 py-3">
        <router-link to="/" class="font-semibold">custos</router-link>
        <RoleGate role="engineer" :workspace-id="workspaceId">
          <router-link :to="`/w/${workspaceId}`" class="text-sm text-gray-600 hover:text-gray-900">
            Dashboard
          </router-link>
          <router-link :to="`/w/${workspaceId}/proposals`" class="text-sm text-gray-600 hover:text-gray-900">
            Proposals
          </router-link>
          <router-link :to="`/w/${workspaceId}/book`" class="text-sm text-gray-600 hover:text-gray-900">
            Book
          </router-link>
        </RoleGate>
        <RoleGate role="task-author">
          <router-link to="/catalog/drafts" class="text-sm text-gray-600 hover:text-gray-900">
            Catalog drafts
          </router-link>
        </RoleGate>
        <RoleGate role="processor-author">
          <router-link to="/processors" class="text-sm text-gray-600 hover:text-gray-900">
            Processors
          </router-link>
        </RoleGate>
        <div class="ml-auto flex items-center gap-3 text-sm">
          <template v-if="auth.principal">
            <span class="text-gray-500">{{ auth.principal.name }}</span>
            <button class="text-gray-600 hover:text-gray-900" @click="auth.logout()">Sign out</button>
          </template>
        </div>
      </nav>
    </header>
    <main class="mx-auto max-w-5xl px-4 py-6">
      <router-view />
    </main>
  </div>
</template>
