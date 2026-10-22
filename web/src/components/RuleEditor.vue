<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import type { Catalog } from '../campaign/types'
import { formProblems, ruleTitle, valueType, type RuleForm } from '../campaign/rules'

// The chip editor (D9). The parent saves on apply; serverErrors are the
// backend's messages for this rule, shown until the form changes.
const props = defineProps<{ open: boolean; form: RuleForm | null; catalog: Catalog; serverErrors?: string[]; saving?: boolean }>()
const emit = defineEmits<{ apply: [RuleForm]; close: [] }>()

const draft = ref<RuleForm | null>(null)
const tried = ref(false)
watch(
  () => props.form,
  (f) => {
    draft.value = f ? { ...f, cities: [...f.cities], categories: [...f.categories] } : null
    tried.value = false
  },
  { immediate: true },
)

const type = computed(() => (draft.value ? valueType(draft.value.field, props.catalog) : undefined))
const ops = computed(() => props.catalog.fields.find((f) => f.field === draft.value?.field)?.ops ?? [])
const problems = computed(() => (draft.value ? formProblems(draft.value, props.catalog) : []))
const shownProblems = computed(() => (tried.value ? problems.value : []).concat(props.serverErrors ?? []))
const hasTime = computed(() => type.value === 'window' || type.value === 'categories_window')
const noEndDate = computed(() => draft.value?.field === 'last_order' && draft.value.op === 'before')
const opLabel: Record<string, string> = { gte: 'at least', lte: 'at most', before: 'longer ago than', after: 'within' }

function toggle(list: string[], value: string) {
  const i = list.indexOf(value)
  if (i >= 0) list.splice(i, 1)
  else list.push(value)
}

function apply() {
  tried.value = true
  if (draft.value && problems.value.length === 0 && !props.saving) emit('apply', draft.value)
}
</script>

<template>
  <DialogRoot :open="open" @update:open="(v) => !v && emit('close')">
    <DialogPortal>
      <DialogOverlay class="overlay" />
      <DialogContent v-if="draft" class="dialog" @submit.prevent>
        <DialogTitle class="title">{{ ruleTitle(draft.field, draft.op) }}</DialogTitle>
        <DialogDescription class="sr-only">Edit the rule, then apply it. The audience count updates when it is saved.</DialogDescription>

        <form class="form" novalidate @submit.prevent="apply">
          <label v-if="ops.length > 1" class="row">
            <span>Condition</span>
            <select v-model="draft.op" class="input small">
              <option v-for="op in ops" :key="op" :value="op">{{ opLabel[op] ?? op }}</option>
            </select>
          </label>

          <fieldset v-if="type === 'cities'" class="choices">
            <legend>Cities</legend>
            <label v-for="city in catalog.cities" :key="city" class="choice">
              <input type="checkbox" :checked="draft.cities.includes(city)" @change="toggle(draft.cities, city)" />
              {{ city }}
            </label>
          </fieldset>

          <fieldset v-if="type === 'categories_window'" class="choices">
            <legend>Categories</legend>
            <label v-for="cat in catalog.categories" :key="cat" class="choice">
              <input type="checkbox" :checked="draft.categories.includes(cat)" @change="toggle(draft.categories, cat)" />
              {{ cat }}
            </label>
          </fieldset>

          <fieldset v-if="hasTime" class="time">
            <legend>When</legend>
            <label class="choice">
              <input v-model="draft.timeMode" type="radio" value="last" />
              {{ noEndDate ? 'More than' : 'In the last' }}
              <input
                v-model.number="draft.lastDays"
                class="input small days"
                type="number"
                min="1"
                :max="catalog.limits.max_days"
                aria-label="Number of days"
                :disabled="draft.timeMode !== 'last'"
              />
              days{{ noEndDate ? ' ago' : '' }}
            </label>
            <p class="hint">Counted back from when sending starts, so the window moves with the send time.</p>
            <label class="choice">
              <input v-model="draft.timeMode" type="radio" value="since" />
              {{ noEndDate ? 'Before' : 'Since' }}
              <input
                v-model="draft.from"
                class="input small"
                type="date"
                :min="catalog.limits.min_date"
                aria-label="Start date"
                :disabled="draft.timeMode !== 'since'"
              />
            </label>
            <label v-if="!noEndDate && draft.timeMode === 'since'" class="choice indent">
              Until (optional)
              <input v-model="draft.to" class="input small" type="date" :min="draft.from || catalog.limits.min_date" aria-label="End date" />
            </label>
            <p class="hint">A fixed date stays fixed, even if the campaign is sent later.</p>
          </fieldset>

          <label v-if="type === 'count' || type === 'amount'" class="row">
            <span>{{ type === 'count' ? 'Orders' : 'Amount (TL)' }}</span>
            <input
              v-model.number="draft.amount"
              class="input small"
              type="number"
              min="0"
              :step="type === 'count' ? 1 : 'any'"
              :max="type === 'count' ? catalog.limits.max_count : catalog.limits.max_amount"
            />
          </label>

          <div v-if="shownProblems.length" role="alert" class="alert alert-error">
            <ul>
              <li v-for="p in shownProblems" :key="p">{{ p }}</li>
            </ul>
          </div>

          <div class="actions">
            <DialogClose class="btn" type="button">Cancel</DialogClose>
            <button type="submit" class="btn btn-primary" :class="{ busy: saving }" :aria-disabled="saving">
              {{ saving ? 'Saving…' : 'Apply' }}
            </button>
          </div>
        </form>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style scoped>
.form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.row {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 14px;
}
.small {
  height: 38px;
  width: auto;
  font-size: 14px;
  padding: 0 10px;
}
.days {
  width: 80px;
}
fieldset {
  margin: 0;
  padding: 0;
  border: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
legend {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-2);
  margin-bottom: 6px;
}
.choices {
  flex-flow: row wrap;
  gap: 8px 16px;
}
.choice {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
}
.choice input[type='checkbox'],
.choice input[type='radio'] {
  width: 16px;
  height: 16px;
  accent-color: var(--primary);
}
.indent {
  margin-left: 24px;
}
.hint {
  margin: -4px 0 0 24px;
  font-size: 12px;
  color: var(--muted);
}
.alert ul {
  margin: 0;
  padding-left: 18px;
}
</style>
