<script setup lang="ts">
// The composer's five steps from the design; earlier steps show a check.
import { t, type Key } from '../i18n'

// With campaignId, the steps a draft can visit (audience, content, review)
// are links.
const props = defineProps<{ current: number; campaignId?: number }>()
const routes = ['campaign', 'campaign-content', 'campaign-review']
const steps: Key[] = ['steps.audience', 'steps.content', 'steps.review', 'steps.sending', 'steps.monitor']
const state = (i: number) => (i + 1 < props.current ? 'done' : i + 1 === props.current ? 'current' : 'todo')
</script>

<template>
  <ol class="steps" :aria-label="t('steps.label')">
    <li v-for="(label, i) in steps" :key="label" :class="state(i)" :aria-current="state(i) === 'current' ? 'step' : undefined">
      <RouterLink v-if="campaignId && i < 3 && i + 1 !== current" :to="{ name: routes[i]!, params: { id: campaignId } }" class="step-link">
        <span class="dot" aria-hidden="true">{{ state(i) === 'done' ? '✓' : i + 1 }}</span>
        <span>{{ t(label) }}<span v-if="state(i) === 'done'" class="sr-only"> {{ t('steps.done') }}</span></span>
      </RouterLink>
      <template v-else>
        <span class="dot" aria-hidden="true">{{ state(i) === 'done' ? '✓' : i + 1 }}</span>
        <span>{{ t(label) }}<span v-if="state(i) === 'done'" class="sr-only"> {{ t('steps.done') }}</span></span>
      </template>
    </li>
  </ol>
</template>

<style scoped>
.steps {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
li {
  flex: 1 1 150px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: var(--radius);
  border: 1px solid var(--line);
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-2);
}
li.current {
  background: var(--surface);
  border-color: var(--primary);
  color: var(--ink);
}
li.done {
  color: var(--ink);
}
.step-link {
  display: flex;
  align-items: center;
  gap: 10px;
  color: inherit;
  text-decoration: none;
}
.step-link:hover span:last-child {
  text-decoration: underline;
}
.dot {
  width: 24px;
  height: 24px;
  flex: 0 0 24px;
  border-radius: 999px;
  background: #efede7;
  color: var(--ink-2);
  font-size: 12px;
  font-weight: 700;
  display: flex;
  align-items: center;
  justify-content: center;
}
.current .dot {
  background: var(--primary);
  color: #fff;
}
.done .dot {
  background: #12866f;
  color: #fff;
}
</style>
