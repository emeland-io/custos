<script setup lang="ts">
defineProps<{ error?: string }>()
</script>

<template>
  <p v-if="error" class="rounded-md border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-800">{{ error }}</p>
</template>
