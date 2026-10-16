<script setup lang="ts" generic="T">
// A list the user reorders by dragging (native HTML5 drag-and-drop, no
// library — ruling 4.16) or with the ↑/↓ buttons. It only accepts drags
// it started itself.
import { ref } from 'vue'

const props = defineProps<{
  items: T[]
  itemKey: (item: T) => string
  disabled?: boolean
}>()

const emit = defineEmits<{
  'update:items': [items: T[]]
}>()

defineSlots<{
  item(props: { item: T; index: number }): any
}>()

const dragIndex = ref<number | null>(null)
const overIndex = ref<number | null>(null)

function move(from: number, to: number) {
  if (from === to || to < 0 || to >= props.items.length) return
  const next = props.items.slice()
  next.splice(to, 0, ...next.splice(from, 1))
  emit('update:items', next)
}

function onDragStart(e: DragEvent, i: number) {
  if (props.disabled) {
    e.preventDefault()
    return
  }
  dragIndex.value = i
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', String(i))
  }
}

function onDragOver(e: DragEvent, i: number) {
  if (dragIndex.value === null) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  overIndex.value = i
}

function onDragLeave(i: number) {
  if (overIndex.value === i) overIndex.value = null
}

function onDrop(e: DragEvent, i: number) {
  if (dragIndex.value === null) return
  e.preventDefault()
  move(dragIndex.value, i)
  dragIndex.value = null
  overIndex.value = null
}

function onDragEnd() {
  dragIndex.value = null
  overIndex.value = null
}
</script>

<template>
  <ol class="space-y-1">
    <li
      v-for="(item, index) in items"
      :key="itemKey(item)"
      :draggable="!disabled"
      class="flex items-center gap-2 rounded border border-gray-200 bg-white px-2 py-1"
      :class="{
        'opacity-50': dragIndex === index,
        'border-t-2 border-t-blue-500': overIndex === index && dragIndex !== index,
      }"
      @dragstart="onDragStart($event, index)"
      @dragover="onDragOver($event, index)"
      @dragleave="onDragLeave(index)"
      @drop="onDrop($event, index)"
      @dragend="onDragEnd"
    >
      <span aria-hidden="true" class="cursor-grab select-none text-gray-400">⋮⋮</span>
      <div class="min-w-0 flex-1">
        <slot name="item" :item="item" :index="index" />
      </div>
      <button
        type="button"
        class="px-1 text-gray-600 disabled:opacity-30"
        :disabled="disabled || index === 0"
        aria-label="Move up"
        @click="move(index, index - 1)"
      >
        ↑
      </button>
      <button
        type="button"
        class="px-1 text-gray-600 disabled:opacity-30"
        :disabled="disabled || index === items.length - 1"
        aria-label="Move down"
        @click="move(index, index + 1)"
      >
        ↓
      </button>
    </li>
  </ol>
</template>
