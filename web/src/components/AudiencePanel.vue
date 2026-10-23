<script setup lang="ts">
import { computed } from 'vue'
import type { Channel, Counts } from '../campaign/types'
import { formatNumber, t } from '../i18n'

// change is the total's difference after the marketer's last edit (shown
// instead of a preview before Apply, which would need another endpoint).
const props = defineProps<{ counts?: Counts; channels: Channel[]; change?: number | null; resend?: boolean }>()
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
.alert {
  margin: 0;
  font-size: 12px;
}
</style>
