<script setup lang="ts">
import { computed } from 'vue'
import type { Channel, Counts } from '../campaign/types'

// change is the total's difference after the marketer's last edit (shown
// instead of a preview before Apply, which would need another endpoint).
const props = defineProps<{ counts?: Counts; channels: Channel[]; change?: number | null; resend?: boolean }>()
const f = (n: number) => n.toLocaleString('en-US')
const rows = computed(() =>
  (['sms', 'email'] as Channel[]).map((ch) => {
    const on = props.channels.includes(ch)
    const n = props.counts?.[ch] ?? 0
    const total = props.counts?.total ?? 0
    return { ch, label: ch === 'sms' ? 'SMS recipients' : 'Email recipients', on, count: on ? f(n) : '—', width: on && total ? `${(n / total) * 100}%` : '0%' }
  }),
)
</script>

<template>
  <section class="card panel" aria-labelledby="audience-title">
    <h2 id="audience-title">Estimated audience</h2>
    <div class="total">
      <span class="number">{{ counts ? f(counts.total) : '—' }}</span>
      <span>customers match right now</span>
      <span v-if="change" class="change" role="status">{{ change > 0 ? '+' : '−' }}{{ f(Math.abs(change)) }} since your last edit</span>
    </div>
    <div v-for="r in rows" :key="r.ch" class="row" :class="{ off: !r.on }">
      <div class="line">
        <span>{{ r.label }}</span><strong>{{ r.count }}</strong>
      </div>
      <div class="bar" aria-hidden="true"><div :style="{ width: r.width }" :class="r.ch" /></div>
      <span v-if="!r.on" class="note">{{ r.ch === 'sms' ? 'SMS' : 'Email' }} not selected</span>
    </div>
    <p class="alert alert-info">
      {{
        resend
          ? 'The recipients of the original campaign who were skipped for the character limit, minus anyone who may already have it.'
          : 'Saved as rules, not a fixed list. Only customers who allow the channel are counted.'
      }}
      The final audience is recalculated when sending starts.
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
.alert {
  margin: 0;
  font-size: 12px;
}
</style>
