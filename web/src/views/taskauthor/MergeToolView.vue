<script setup lang="ts">
// The merge tool (§6.1): for each path the publish could not merge, the
// author keeps the published version (ours = catalog main), takes the
// draft's (theirs), or edits the file by hand (side "content"). It only
// collects choices; DraftsView sends them.
import { computed, ref, watch } from 'vue'
import LineDiff from './LineDiff.vue'
import { encodeBase64Text } from '../../api/authors'
import type { DecodedConflict, ResolutionJSON } from '../../types/taskAuthor'

type Choice = 'ours' | 'theirs' | 'edit'

const props = defineProps<{
  conflicts: DecodedConflict[]
  busy: boolean
}>()

const emit = defineEmits<{
  resolve: [resolutions: Record<string, ResolutionJSON>]
  cancel: []
}>()

const choices = ref<Record<string, Choice | undefined>>({})
const edits = ref<Record<string, string>>({})

// A new conflicts array is a new round: forget every earlier choice.
watch(
  () => props.conflicts,
  (cs) => {
    const c: Record<string, Choice | undefined> = {}
    const e: Record<string, string> = {}
    for (const x of cs) {
      c[x.path] = undefined
      e[x.path] = x.theirs ?? x.ours ?? '' // editing starts from the draft's text
    }
    choices.value = c
    edits.value = e
  },
  { immediate: true },
)

const unresolved = computed(() => props.conflicts.filter((c) => choices.value[c.path] === undefined).length)

function submit() {
  if (props.busy || unresolved.value > 0) return
  const out: Record<string, ResolutionJSON> = {}
  for (const c of props.conflicts) {
    const choice = choices.value[c.path]
    if (choice === 'ours') out[c.path] = { side: 'ours' }
    else if (choice === 'theirs') out[c.path] = { side: 'theirs' }
    else if (choice === 'edit') out[c.path] = { side: 'content', content: encodeBase64Text(edits.value[c.path] ?? '') }
  }
  emit('resolve', out)
}
</script>

<template>
  <section class="space-y-4 rounded border border-amber-300 bg-amber-50 p-4">
    <header class="space-y-1">
      <h3 class="text-lg font-semibold">Resolve conflicts with the published catalog</h3>
      <p class="text-sm text-gray-700">
        The published catalog changed since this draft was created, and {{ conflicts.length }}
        {{ conflicts.length === 1 ? 'file was' : 'files were' }} changed on both sides. Choose which version of each
        file to publish. The diff shows the published version (−) against this draft's (+).
      </p>
    </header>

    <article v-for="c in conflicts" :key="c.path" class="space-y-2 rounded border border-gray-200 bg-white p-3">
      <h4 class="font-mono text-sm">
        {{ c.path }} <span class="text-xs text-gray-500">({{ c.kind }})</span>
      </h4>
      <fieldset class="flex flex-wrap gap-4 text-sm" :disabled="busy">
        <legend class="sr-only">Resolution for {{ c.path }}</legend>
        <label class="flex items-center gap-1">
          <input v-model="choices[c.path]" type="radio" :name="'resolution-' + c.path" value="ours" />
          Keep the published version{{ c.ours === null ? ' (the file stays deleted)' : '' }}
        </label>
        <label class="flex items-center gap-1">
          <input v-model="choices[c.path]" type="radio" :name="'resolution-' + c.path" value="theirs" />
          Take this draft's version{{ c.theirs === null ? ' (deletes the file)' : '' }}
        </label>
        <label class="flex items-center gap-1">
          <input v-model="choices[c.path]" type="radio" :name="'resolution-' + c.path" value="edit" />
          Edit by hand
        </label>
      </fieldset>
      <LineDiff :before="c.ours ?? ''" :after="c.theirs ?? ''" />
      <textarea
        v-if="choices[c.path] === 'edit'"
        v-model="edits[c.path]"
        rows="12"
        class="w-full rounded border border-gray-300 p-2 font-mono text-sm"
        :disabled="busy"
      />
    </article>

    <footer class="flex flex-wrap items-center gap-3">
      <button
        type="button"
        class="rounded bg-blue-600 px-4 py-2 text-white disabled:opacity-50"
        :disabled="busy || unresolved > 0"
        @click="submit"
      >
        {{ busy ? 'Publishing…' : 'Publish with these resolutions' }}
      </button>
      <button type="button" class="rounded px-4 py-2 text-gray-700 hover:bg-gray-100" :disabled="busy" @click="emit('cancel')">
        Cancel
      </button>
      <span v-if="unresolved > 0" class="text-sm text-gray-600">
        {{ unresolved }} {{ unresolved === 1 ? 'file still needs' : 'files still need' }} a choice.
      </span>
    </footer>
  </section>
</template>
