<script setup lang="ts">
import { computed } from 'vue'
import { useAuthStore } from '../stores/auth'

const props = defineProps<{
  role: 'admin' | 'task-author' | 'processor-author' | 'engineer'
  workspaceId?: string
}>()

const auth = useAuthStore()
const allowed = computed(() => auth.has(props.role, props.workspaceId))
</script>

<template>
  <slot v-if="allowed" />
</template>
