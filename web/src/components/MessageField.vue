<script setup lang="ts">
import { ref } from 'vue'
import { insertVariable, VARIABLES, type Variable } from '../content/variables'
import { formatNumber, t } from '../i18n'

// One editable message field. length is the text with the longest fictional
// sample filled in, so the counter shows the realistic length (D3).
const props = defineProps<{
  id: string
  label: string
  modelValue: string
  multiline?: boolean
  rows?: number
  limit: number
  length: number
  readonly?: boolean
  errors?: string[]
  variables?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [string]; commit: [] }>()
const input = ref<HTMLTextAreaElement | HTMLInputElement>()

function insert(v: Variable) {
  const el = input.value
  const start = el?.selectionStart ?? props.modelValue.length
  const end = el?.selectionEnd ?? start
  const next = insertVariable(props.modelValue, start, end, v)
  emit('update:modelValue', next.text)
  requestAnimationFrame(() => {
    el?.focus()
    el?.setSelectionRange(next.cursor, next.cursor)
  })
  emit('commit')
}
const token = (v: Variable) => `{{${v}}}`
</script>

<template>
  <div class="field-block">
    <div class="head">
      <label :for="id">{{ label }}</label>
      <span :id="`${id}-count`" class="count" :class="{ over: length > limit }">{{ t('content.longest', { count: formatNumber(length), max: formatNumber(limit) }) }}</span>
    </div>
    <textarea
      v-if="multiline"
      :id="id"
      ref="input"
      class="textarea"
      :rows="rows ?? 4"
      :value="modelValue"
      :readonly="readonly"
      :aria-invalid="!!errors?.length"
      :aria-describedby="`${id}-count${errors?.length ? ` ${id}-errors` : ''}`"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
      @blur="emit('commit')"
    />
    <input
      v-else
      :id="id"
      ref="input"
      class="input"
      :value="modelValue"
      :readonly="readonly"
      :aria-invalid="!!errors?.length"
      :aria-describedby="`${id}-count${errors?.length ? ` ${id}-errors` : ''}`"
      @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
      @blur="emit('commit')"
    />
    <div v-if="variables" class="vars" role="group" :aria-label="t('content.insert')">
      <span aria-hidden="true">{{ t('content.insert') }}</span>
      <button
        v-for="v in VARIABLES"
        :key="v"
        type="button"
        class="var"
        :disabled="readonly"
        :aria-label="t('content.insertVar', { name: t(`var.${v}`) })"
        @mousedown.prevent
        @click="insert(v)"
      >
        {{ token(v) }}
      </button>
    </div>
    <ul v-if="errors?.length" :id="`${id}-errors`" class="errors" role="alert">
      <li v-for="e in errors" :key="e">{{ e }}</li>
    </ul>
  </div>
</template>

<style scoped>
.field-block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.head {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-2);
}
.count {
  font-weight: 500;
  color: var(--muted);
}
.count.over {
  color: var(--danger);
  font-weight: 600;
}
.textarea {
  width: 100%;
  resize: vertical;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius);
  padding: 12px;
  background: var(--surface-muted);
  font: 400 14px/1.55 var(--font);
  color: var(--ink);
}
.input {
  height: 40px;
  font-size: 14px;
  background: var(--surface-muted);
}
.textarea[aria-invalid='true'],
.input[aria-invalid='true'] {
  border: 1.5px solid var(--danger-line);
}
.textarea[readonly],
.input[readonly] {
  color: var(--ink-2);
}
.vars {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--muted);
}
.var {
  height: 28px;
  padding: 0 8px;
  border: 1px solid var(--line-strong);
  border-radius: 6px;
  background: var(--surface);
  font: 500 12px var(--mono);
  color: var(--primary-ink);
  cursor: pointer;
}
.errors {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
  color: var(--danger);
}
</style>
