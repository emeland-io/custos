<script setup lang="ts">
import { computed } from 'vue'
import Badge from './Badge.vue'
import type { VerificationStatus } from '../api'

const props = defineProps<{ status: VerificationStatus; met?: boolean }>()

const tone = computed(() => {
  if (props.status === 'VERIFIED') return props.met === false ? 'warn' : 'good'
  if (props.status === 'FAILED') return 'bad'
  return 'warn'
})
</script>

<template>
  <Badge :tone="tone">
    {{ status.toLowerCase() }}<template v-if="status === 'VERIFIED' && met === false"> · requirement not met</template>
  </Badge>
</template>
