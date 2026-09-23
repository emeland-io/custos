<script setup lang="ts">
import { RouterLink, RouterView } from 'vue-router'

const links = [
  { to: '/roots', label: 'Roots' },
  { to: '/seeds', label: 'Seeds' },
  { to: '/keys', label: 'Keys' },
]
</script>

<template>
  <header class="border-b border-stone-200 bg-white">
    <nav class="mx-auto flex max-w-5xl items-center gap-6 px-4 py-3">
      <RouterLink to="/" class="text-lg font-bold tracking-tight text-emerald-800">custos</RouterLink>
      <RouterLink
        v-for="l in links"
        :key="l.to"
        :to="l.to"
        class="text-sm font-medium text-stone-600 hover:text-stone-900"
        active-class="!text-emerald-800"
      >
        {{ l.label }}
      </RouterLink>
    </nav>
  </header>
  <main class="mx-auto max-w-5xl px-4 py-6">
    <RouterView :key="$route.fullPath" />
  </main>
</template>
