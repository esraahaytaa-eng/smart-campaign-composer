<script setup lang="ts">
import { ref, watch } from 'vue'

// The campaign name as an editable heading (D42): saved on Enter or when the
// field loses focus; Escape restores the saved name.
const props = defineProps<{ name: string; disabled?: boolean }>()
const emit = defineEmits<{ rename: [string] }>()
const value = ref(props.name)
const error = ref('')
watch(
  () => props.name,
  (n) => (value.value = n),
)

function commit() {
  const name = value.value.trim()
  error.value = name.length === 0 || [...name].length > 100 ? 'The name must be 1 to 100 characters.' : ''
  if (!error.value && name !== props.name) emit('rename', name)
}
function revert(e: KeyboardEvent) {
  value.value = props.name
  error.value = ''
  ;(e.target as HTMLInputElement).blur()
}
</script>

<template>
  <div class="name">
    <label for="campaign-name" class="sr-only">Campaign name</label>
    <input
      id="campaign-name"
      v-model="value"
      class="name-input"
      maxlength="100"
      :disabled="disabled"
      :aria-invalid="!!error"
      :aria-describedby="error ? 'campaign-name-error' : undefined"
      @blur="commit"
      @keydown.enter.prevent="($event.target as HTMLInputElement).blur()"
      @keydown.esc="revert"
    />
    <span v-if="error" id="campaign-name-error" class="field-error">{{ error }}</span>
  </div>
</template>

<style scoped>
.name {
  flex: 1 1 260px;
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.name-input {
  width: 100%;
  height: 42px;
  border: 1px solid transparent;
  border-radius: 9px;
  padding: 0 8px;
  background: transparent;
  font: 700 22px var(--font);
  color: var(--ink);
}
.name-input:hover {
  border-color: var(--line-strong);
}
.name-input[aria-invalid='true'] {
  border-color: var(--danger-line);
}
</style>
