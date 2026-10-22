<script setup lang="ts">
import type { Channel } from '../campaign/types'

const props = defineProps<{ modelValue: Channel[]; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [Channel[]] }>()

const options: { label: string; channels: Channel[] }[] = [
  { label: 'SMS', channels: ['sms'] },
  { label: 'Email', channels: ['email'] },
  { label: 'Both', channels: ['sms', 'email'] },
]
const selected = (o: { channels: Channel[] }) =>
  o.channels.length === props.modelValue.length && o.channels.every((c) => props.modelValue.includes(c))
</script>

<template>
  <div class="picker" role="group" aria-labelledby="channels-label">
    <span id="channels-label" class="label">Channels</span>
    <div class="options">
      <button
        v-for="o in options"
        :key="o.label"
        type="button"
        :aria-pressed="selected(o)"
        :disabled="disabled"
        @click="!selected(o) && emit('update:modelValue', o.channels)"
      >
        {{ o.label }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  align-items: center;
  gap: 10px;
}
.label {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-2);
}
.options {
  display: inline-flex;
  border: 1px solid var(--line-strong);
  border-radius: 9px;
  overflow: hidden;
}
button {
  height: 38px;
  padding: 0 16px;
  border: 0;
  background: var(--surface);
  color: var(--ink-2);
  font: 600 14px var(--font);
  cursor: pointer;
}
button + button {
  border-left: 1px solid var(--line-strong);
}
button[aria-pressed='true'] {
  background: var(--ink);
  color: #fff;
}
button:disabled {
  cursor: default;
  opacity: 0.6;
}
</style>
