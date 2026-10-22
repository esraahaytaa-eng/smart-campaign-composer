<script setup lang="ts">
import { computed } from 'vue'
import { DropdownMenuContent, DropdownMenuItem, DropdownMenuPortal, DropdownMenuRoot, DropdownMenuTrigger } from 'reka-ui'
import type { Catalog, Rule } from '../campaign/types'
import { addableRules, ruleLabel, ruleTitle } from '../campaign/rules'

const props = defineProps<{ rules: Rule[]; catalog: Catalog; errors?: Map<number, string[]>; disabled?: boolean }>()
const emit = defineEmits<{ edit: [index: number]; remove: [index: number]; add: [field: string, op: string] }>()

const addable = computed(() => addableRules(props.rules, props.catalog))
const lastRule = computed(() => props.rules.length <= 1) // the backend needs at least one rule
</script>

<template>
  <div class="chips">
    <template v-for="(r, i) in rules" :key="`${r.field} ${r.op}`">
      <span v-if="i > 0" class="and" aria-hidden="true">AND</span>
      <span class="chip" :class="{ invalid: errors?.has(i) }">
        <button type="button" class="chip-label" :disabled="disabled" :aria-describedby="errors?.has(i) ? `rule-error-${i}` : undefined" @click="emit('edit', i)">
          {{ ruleLabel(r) }}<span class="sr-only">. Edit</span>
        </button>
        <!-- aria-disabled keeps the button focusable, so its reason can be heard. -->
        <button
          type="button"
          class="chip-remove"
          :disabled="disabled"
          :aria-disabled="lastRule"
          :aria-describedby="lastRule ? 'last-rule-hint' : undefined"
          :aria-label="`Remove rule: ${ruleLabel(r)}`"
          @click="!lastRule && emit('remove', i)"
        >
          ×
        </button>
        <span v-if="errors?.has(i)" :id="`rule-error-${i}`" class="chip-error">{{ errors.get(i)?.join(' ') }}</span>
      </span>
    </template>
    <span class="and" aria-hidden="true">AND</span>
    <span class="chip fixed" title="Always applied. Customers without consent for a channel never receive it.">
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="5" y="11" width="14" height="10" rx="2" /><path d="M8 11V8a4 4 0 0 1 8 0v3" /></svg>
      Has consent for the channel<span class="sr-only"> (always applied)</span>
    </span>
    <span v-if="lastRule" id="last-rule-hint" class="hint">A campaign needs at least one audience rule.</span>
    <DropdownMenuRoot v-if="addable.length">
      <DropdownMenuTrigger class="add" :disabled="disabled">+ Add rule</DropdownMenuTrigger>
      <DropdownMenuPortal>
        <DropdownMenuContent class="menu add-menu" align="start" :side-offset="6">
          <DropdownMenuItem v-for="p in addable" :key="`${p.field} ${p.op}`" class="menu-item" @select="emit('add', p.field, p.op)">
            {{ ruleTitle(p.field, p.op) }}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenuPortal>
    </DropdownMenuRoot>
  </div>
</template>

<style scoped>
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}
.and {
  font-size: 11px;
  font-weight: 700;
  color: var(--muted);
}
.chip {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  min-height: 36px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  background: var(--surface-muted);
  font-size: 14px;
}
.chip.invalid {
  border: 1.5px solid var(--danger-line);
}
.chip.fixed {
  gap: 6px;
  padding: 0 12px;
  border-style: dashed;
  border-color: #c7c3b8;
  background: transparent;
  color: var(--ink-2);
}
.chip-label {
  min-height: 34px;
  padding: 0 4px 0 12px;
  border: 0;
  background: transparent;
  font: 500 14px var(--font);
  color: var(--ink);
  text-align: left;
  cursor: pointer;
}
.chip-remove {
  width: 30px;
  height: 34px;
  border: 0;
  background: transparent;
  color: var(--muted);
  font-size: 18px;
  cursor: pointer;
}
.hint {
  flex-basis: 100%;
  font-size: 12px;
  color: var(--muted);
}
.chip-remove[aria-disabled='true'],
.chip-remove:disabled,
.chip-label:disabled {
  cursor: default;
  opacity: 0.55;
}
.chip-error {
  flex-basis: 100%;
  padding: 0 12px 6px;
  font-size: 12px;
  color: var(--danger);
}
.add {
  height: 36px;
  padding: 0 12px;
  border-radius: var(--radius-sm);
  border: 1px dashed #a8a4e8;
  background: transparent;
  color: var(--link);
  font: 600 13px var(--font);
  cursor: pointer;
}
.add-menu {
  max-height: 320px;
  overflow: auto;
}
</style>
