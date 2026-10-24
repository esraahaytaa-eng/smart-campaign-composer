<script setup lang="ts">
import { computed } from 'vue'
import type { Catalog, Channel, Counts, Lang } from '../campaign/types'
import { audienceByLanguage } from '../content/languages'
import { formatNumber, t } from '../i18n'

// change is the total's difference after the marketer's last edit (shown
// instead of a preview before Apply, which would need another endpoint).
const props = defineProps<{
  counts?: Counts
  channels: Channel[]
  change?: number | null
  resend?: boolean
  languages?: Catalog['languages']
  recommended?: Lang[]
  selected?: Lang[] // given: the languages can be ticked here (D71)
}>()
defineEmits<{ toggle: [lang: Lang, on: boolean] }>()
const byLanguage = computed(() => audienceByLanguage(props.counts, props.channels, props.recommended ?? [], props.languages ?? []))
const rows = computed(() =>
  (['sms', 'email'] as Channel[]).map((ch) => {
    const on = props.channels.includes(ch)
    const n = props.counts?.[ch] ?? 0
    const total = props.counts?.total ?? 0
    return {
      ch,
      label: t(ch === 'sms' ? 'channel.recipients.sms' : 'channel.recipients.email'),
      off: t('channel.notSelected', { channel: t(ch === 'sms' ? 'channel.sms' : 'channel.email') }),
      on,
      count: on ? formatNumber(n) : '—',
      width: on && total ? `${(n / total) * 100}%` : '0%',
    }
  }),
)
</script>

<template>
  <section class="card panel" aria-labelledby="audience-title">
    <h2 id="audience-title">{{ t('audience.title') }}</h2>
    <div class="total">
      <span class="number">{{ counts ? formatNumber(counts.total) : '—' }}</span>
      <span>{{ t('audience.match') }}</span>
      <span v-if="change" class="change" role="status">{{ t('audience.change', { sign: change > 0 ? '+' : '−', count: formatNumber(Math.abs(change)) }) }}</span>
    </div>
    <div v-for="r in rows" :key="r.ch" class="row" :class="{ off: !r.on }">
      <div class="line">
        <span>{{ r.label }}</span><strong>{{ r.count }}</strong>
      </div>
      <div class="bar" aria-hidden="true"><div :style="{ width: r.width }" :class="r.ch" /></div>
      <span v-if="!r.on" class="note">{{ r.off }}</span>
    </div>
    <div v-if="byLanguage.length" class="languages">
      <h3>{{ t('audience.byLanguage') }}</h3>
      <ul>
        <li v-for="l in byLanguage" :key="l.code">
          <label v-if="selected" class="lang">
            <input type="checkbox" :checked="selected.includes(l.code)" @change="$emit('toggle', l.code, ($event.target as HTMLInputElement).checked)" />
            <strong>{{ l.name }}</strong> {{ l.share }}%
            <span v-if="l.recommended" class="tag">{{ t('content.recommended') }}</span>
          </label>
          <span v-else class="lang">
            <strong>{{ l.name }}</strong> {{ l.share }}%
            <span v-if="l.recommended" class="tag">{{ t('content.recommended') }}</span>
          </span>
          <span class="muted">
            <template v-if="l.sms !== null">{{ t('channel.sms') }} {{ formatNumber(l.sms) }}</template>
            <template v-if="l.sms !== null && l.email !== null"> · </template>
            <template v-if="l.email !== null">{{ t('channel.email') }} {{ formatNumber(l.email) }}</template>
          </span>
        </li>
      </ul>
      <p class="note">{{ t(selected ? 'audience.languagesPick' : 'audience.languagesNote') }}</p>
    </div>
    <p class="alert alert-info">
      {{ t(resend ? 'audience.resendNote' : 'audience.rulesNote') }}
      {{ t('audience.recalculated') }}
    </p>
  </section>
</template>

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 650;
}
.total {
  display: flex;
  flex-direction: column;
  gap: 2px;
  color: var(--ink-2);
}
.number {
  font-size: 44px;
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: 1;
  color: var(--ink);
}
.change {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-2); /* neutral: a smaller audience isn't an error */
}
.row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.row.off {
  opacity: 0.45;
}
.line {
  display: flex;
  justify-content: space-between;
}
.bar {
  height: 8px;
  border-radius: 999px;
  background: #efede7;
  overflow: hidden;
}
.bar div {
  height: 100%;
}
.bar .sms {
  background: var(--primary);
}
.bar .email {
  background: var(--ink);
}
.note {
  font-size: 12px;
  color: var(--muted);
}
.languages h3 {
  margin: 0 0 8px;
  font-size: 13px;
  font-weight: 650;
}
.languages ul {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
}
.languages li {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 4px 12px;
}
.languages label {
  display: flex;
  align-items: center;
  gap: 6px;
}
.languages .muted {
  color: var(--muted);
}
.tag {
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.alert {
  margin: 0;
  font-size: 12px;
}
</style>
