<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { campaigns } from '../campaign/client'
import { ruleLabel } from '../campaign/rules'
import { briefTitle } from '../campaign/title'
import type { Campaign, Channel } from '../campaign/types'
import { channelRows, deliveryPhase, deliverySummary, type Group, type Snapshot } from '../delivery/status'
import { writeQuery } from '../delivery/recipients'
import { watchStatus } from '../delivery/stream'
import { formatDateTime, formatNumber, formatPercent, formatTime, isKey, t } from '../i18n'
import { errorText } from '../i18n/errors'
import CancelDialog from './CancelDialog.vue'
import RecipientsDrawer from './RecipientsDrawer.vue'

// A campaign after approval (boards 4 and B): scheduled, live delivery, or
// read-only results. Everything comes from the status stream (D36).
const props = defineProps<{ campaign: Campaign; parent: Campaign | null }>()
const emit = defineEmits<{ updated: [Campaign]; status: [Snapshot['status']] }>()

const snap = ref<Snapshot | null>(null)
const asOf = ref<Date | null>(null)
const ended = ref(false)
const paused = ref(false)
const reconnecting = ref(false)
const cancelling = ref<Campaign | null>(null)
const actionError = ref<unknown>(null)

let stream: { close: () => void } | null = null
function connect() {
  stream?.close()
  ended.value = false
  stream = watchStatus(props.campaign.id, {
    onStatus: (s) => {
      snap.value = s
      asOf.value = new Date()
      emit('status', s.status)
    },
    onEnd: () => (ended.value = true),
    onTrouble: (r) => (reconnecting.value = r),
  })
}
connect()
onBeforeUnmount(() => stream?.close())

function togglePause() {
  paused.value = !paused.value
  if (paused.value) {
    stream?.close()
    reconnecting.value = false
  } else connect()
}

async function cancel(c: Campaign) {
  cancelling.value = null
  actionError.value = null
  try {
    emit('updated', await campaigns.cancel(c.id))
  } catch (err) {
    actionError.value = err
  }
  if (!paused.value) connect() // the stream ends once nothing is in flight
}

const phase = computed(() => (snap.value ? deliveryPhase(snap.value) : null))
const summary = computed(() => (snap.value ? deliverySummary(snap.value) : null))
const live = computed(() => phase.value === 'sending' && !ended.value)
const cancellable = computed(() => snap.value?.status === 'approved' || snap.value?.status === 'sending')

const pct = formatPercent
const channelName = (ch: Channel) => t(`channel.${ch}`)
const split = (sms?: number, email?: number) => {
  const chs = props.campaign.channels
  if (chs.length === 2) return t('delivery.split', { sms: formatNumber(sms ?? 0), email: formatNumber(email ?? 0) })
  return chs[0] === 'sms' ? t('delivery.splitSms', { sms: formatNumber(sms ?? 0) }) : t('delivery.splitEmail', { email: formatNumber(email ?? 0) })
}
const reasonText = (code: string) => {
  const key = `reason.${code}`
  return isKey(key) ? t(key) : code
}
const groupSub = (group: Group, reasons: { code: string; count: number }[]) => {
  if (reasons.length) return reasons.map((r) => t('reason.count', { count: formatNumber(r.count), reason: reasonText(r.code) })).join(' · ')
  const key = `groupSub.${group}`
  return isKey(key) ? t(key) : ''
}
const delta = computed(() => {
  const s = summary.value
  if (!s?.estimate || !s.actual) return ''
  const d = s.actual.messages - s.estimate.messages
  return d === 0 ? '' : `(${d > 0 ? '+' : '−'}${formatNumber(Math.abs(d))})`
})
const rules = computed(() => (props.campaign.rules ?? []).map(ruleLabel))
// Each count opens recipient details for its group (board 5).
const open = (group: Group, channel: Channel | 'all' = 'all') => ({ query: writeQuery({ group, channel, q: '', page: 1 }) })
</script>

<template>
  <div class="delivery">
    <div v-if="actionError" role="alert" class="alert alert-error">{{ errorText(actionError) }}</div>
    <div v-if="parent || campaign.parent_campaign_id" class="alert alert-info link-line">
      {{ t('delivery.resendOf', { title: parent ? briefTitle(parent.request_text) : `#${campaign.parent_campaign_id}` }) }}
      <RouterLink :to="{ name: 'campaign', params: { id: campaign.parent_campaign_id! } }">{{ t('delivery.openOriginal') }}</RouterLink>
    </div>

    <p v-if="!snap" class="loading" role="status">{{ t('delivery.loading') }}</p>

    <!-- scheduled: approved, sending not started -->
    <div v-else-if="phase === 'scheduled'" class="scheduled">
      <div class="main">
        <section class="card section" aria-labelledby="aud-h">
          <h2 id="aud-h">{{ t('delivery.audience') }}</h2>
          <ul v-if="rules.length" class="rules">
            <li v-for="r in rules" :key="r">{{ r }}</li>
            <li>{{ t('rules.consent') }}</li>
          </ul>
          <div class="est-grid">
            <div class="est">
              <span class="kicker">{{ t('delivery.estimated') }}</span>
              <span class="big">{{ formatNumber(snap.approval_estimate?.total ?? 0) }}</span>
              <span class="muted">{{ split(snap.approval_estimate?.sms, snap.approval_estimate?.email) }}</span>
            </div>
            <div class="est">
              <span class="kicker">{{ t('delivery.actual') }}</span>
              <span class="big">—</span>
              <span class="muted">{{ t('delivery.calculatedAt', { time: snap.send_at ? formatDateTime(snap.send_at) : '' }) }}</span>
            </div>
          </div>
        </section>
        <section v-if="campaign.content" class="card section" aria-labelledby="content-h">
          <h2 id="content-h">{{ t('delivery.content') }}</h2>
          <div v-if="campaign.content.sms" class="message">
            <span class="kicker">{{ t('channel.sms') }}</span>
            <p class="text">{{ campaign.content.sms.body }}</p>
          </div>
          <div v-if="campaign.content.email" class="message">
            <span class="kicker">{{ t('channel.email') }} · {{ campaign.content.email.subject }}</span>
            <p class="text">{{ campaign.content.email.body }}</p>
          </div>
        </section>
      </div>
      <aside class="card section side">
        <span class="kicker">{{ t('delivery.sendingStarts') }}</span>
        <span class="when">{{ snap.send_at ? formatDateTime(snap.send_at) : '—' }}</span>
        <span v-if="campaign.approved_at" class="muted">{{ t('delivery.approvedAt', { time: formatDateTime(campaign.approved_at) }) }}</span>
        <button type="button" class="btn btn-danger-outline" @click="cancelling = campaign">{{ t('delivery.cancel') }}</button>
        <span class="muted small">{{ t('delivery.noEdits') }}</span>
      </aside>
    </div>

    <!-- sending, completed or cancelled -->
    <template v-else-if="summary">
      <div class="toolbar">
        <h2 class="toolbar-title">{{ t('delivery.title') }}</h2>
        <span v-if="live" class="pill live"><span class="pulse" aria-hidden="true" />{{ t('delivery.live') }}</span>
        <span v-else-if="ended" class="pill">{{ t('delivery.readOnly') }}</span>
        <span v-if="asOf" class="muted">{{ t('delivery.asOf', { time: formatTime(asOf) }) }}</span>
        <span class="spacer" />
        <button v-if="phase === 'sending' && !ended" type="button" class="btn btn-sm" :aria-pressed="paused" @click="togglePause">
          {{ paused ? t('delivery.resume') : t('delivery.pause') }}
        </button>
        <button v-if="cancellable" type="button" class="btn btn-sm btn-danger-outline" @click="cancelling = campaign">{{ t('delivery.cancel') }}</button>
      </div>
      <p v-if="reconnecting" role="status" class="alert alert-warning">{{ t('delivery.reconnecting') }}</p>

      <section class="card audience" :aria-label="t('delivery.audience')">
        <span class="kicker">{{ t('delivery.audience') }}</span>
        <span>{{ t('delivery.estimated') }} <strong>{{ summary.estimate ? formatNumber(summary.estimate.messages) : '—' }}</strong></span>
        <span v-if="summary.actual">{{ t('delivery.actual') }} <strong>{{ formatNumber(summary.actual.messages) }}</strong> <span class="muted">{{ delta }}</span></span>
        <span v-else>{{ t('delivery.neverCalculated') }}</span>
        <span v-if="summary.actual && snap.audience_built_at" class="muted">
          {{ t('delivery.recalculated', { time: formatDateTime(snap.audience_built_at) }) }} · {{ split(summary.actual.sms, summary.actual.email) }}
        </span>
        <span class="muted small note">{{ t('delivery.estimateNote') }}</span>
      </section>

      <section v-if="phase === 'cancelledBeforeSending'" class="card section">{{ t('delivery.noMessages') }}</section>

      <template v-else>
        <div class="summary">
          <div class="card stat total">
            <span class="kicker">{{ t('delivery.total') }}</span>
            <span class="value">{{ formatNumber(summary.total) }}</span>
            <span class="muted">{{ split(summary.actual?.sms, summary.actual?.email) }}</span>
            <div class="progress" aria-hidden="true"><div :style="{ width: `${summary.finishedPct}%` }" /></div>
            <span class="muted">{{ t('delivery.finished', { count: formatNumber(summary.finished), pct: pct(summary.finishedPct) }) }}</span>
          </div>
          <RouterLink :to="open('sent')" class="card stat">
            <span class="kicker"><span class="dot sent" aria-hidden="true" />{{ t('delivery.sent') }}</span>
            <span class="value">{{ formatNumber(summary.sent) }}</span>
            <span class="muted">{{ t('delivery.sentPct', { pct: pct(summary.total ? (summary.sent / summary.total) * 100 : 0) }) }}</span>
            <span class="muted small">{{ t('delivery.sentNote') }}</span>
          </RouterLink>
          <div class="card stat">
            <span class="kicker">{{ t('delivery.exceptions') }}</span>
            <span class="value">{{ formatNumber(summary.notSent + summary.uncertain) }}</span>
            <RouterLink :to="open('failed')" class="pair"><span><span class="dot failed" aria-hidden="true" />{{ t('delivery.notSent') }} <span class="muted">· {{ t('delivery.notSentSub') }}</span></span><strong>{{ formatNumber(summary.notSent) }}</strong></RouterLink>
            <RouterLink :to="open('uncertain')" class="pair"><span><span class="dot uncertain" aria-hidden="true" />{{ t('delivery.unknown') }} <span class="muted">· {{ t('delivery.unknownSub') }}</span></span><strong>{{ formatNumber(summary.uncertain) }}</strong></RouterLink>
          </div>
          <RouterLink :to="open('pending')" class="card stat">
            <span class="kicker"><span class="dot retrying" aria-hidden="true" />{{ t('delivery.inProgress') }}</span>
            <span class="value">{{ formatNumber(summary.inProgress) }}</span>
            <span class="muted">
              {{ t('delivery.inProgressSub', { pending: formatNumber(summary.pending), retrying: formatNumber(summary.retrying) }) }}
            </span>
            <span class="muted small">{{ t('delivery.inProgressNote') }}</span>
          </RouterLink>
        </div>

        <div class="channels">
          <section v-for="c in summary.channels" :key="c.channel" class="card section" :aria-labelledby="`ch-${c.channel}`">
            <div class="ch-head">
              <h2 :id="`ch-${c.channel}`">{{ channelName(c.channel) }} <span class="muted">{{ t('delivery.channelTotal', { count: formatNumber(c.status.audience) }) }}</span></h2>
              <span class="muted">{{ t('delivery.processed', { pct: pct(c.finishedPct) }) }}</span>
            </div>
            <div
              class="bar"
              role="img"
              :aria-label="t('delivery.bar', { channel: channelName(c.channel), sent: c.status.groups.sent, failed: c.status.groups.failed, skipped: c.status.groups.skipped, uncertain: c.status.groups.uncertain, retrying: c.status.groups.retrying, pending: c.status.groups.pending })"
            >
              <div v-for="s in c.segments" :key="s.group" :class="['seg', s.group]" :style="{ width: `${(s.count / (c.status.audience || 1)) * 100}%` }" />
            </div>
            <div class="rows">
              <RouterLink
                v-for="(r, i) in channelRows(c.status)"
                :key="r.group"
                :to="open(r.group, c.channel)"
                class="channel-row"
                :class="{ first: i === 0 || channelRows(c.status)[i - 1]!.section !== r.section }"
                :aria-label="t('delivery.openGroup', { channel: channelName(c.channel), group: t(`group.${r.group}`), count: formatNumber(r.count) })"
              >
                <span class="section-name">{{ i === 0 || channelRows(c.status)[i - 1]!.section !== r.section ? t(`section.${r.section}`) : '' }}</span>
                <span class="label"><span :class="['dot', r.group]" aria-hidden="true" />{{ t(`group.${r.group}`) }} <span class="muted">{{ groupSub(r.group, r.reasons) }}</span></span>
                <strong>{{ formatNumber(r.count) }}</strong>
              </RouterLink>
            </div>
            <p class="muted small">
              {{ t(`delivery.rate.${c.channel}`, { count: c.status.rate_limited_recent }) }}
              {{ t('delivery.rateTotal', { retries: formatNumber(c.status.retries), limited: formatNumber(c.status.rate_limited) }) }}
            </p>
            <p v-if="c.status.active_resend_id" class="link-line">
              {{ t('delivery.activeResend', { channel: channelName(c.channel) }) }}
              <RouterLink :to="{ name: 'campaign', params: { id: c.status.active_resend_id } }">{{ t('delivery.openResend') }}</RouterLink>
            </p>
          </section>
        </div>
        <p class="muted small">{{ t('delivery.footnote') }}</p>
      </template>
    </template>

    <CancelDialog :campaign="cancelling" @confirm="cancel" @close="cancelling = null" />
    <RecipientsDrawer v-if="snap && phase !== 'scheduled'" :campaign="campaign" />
  </div>
</template>

<style scoped>
.delivery {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.loading {
  color: var(--ink-2);
}
.muted {
  color: var(--muted);
  font-size: 13px;
}
.small {
  font-size: 12px;
}
.kicker {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.02em;
  color: var(--ink-2);
}
h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 650;
}
.section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.link-line a {
  color: var(--link);
  font-weight: 600;
}
.scheduled {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 300px;
  gap: 16px;
  align-items: start;
}
.main {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.rules {
  margin: 0;
  padding-left: 18px;
  font-size: 14px;
  color: var(--ink-2);
}
.est-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}
.est {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 14px;
  border-radius: var(--radius);
  background: var(--surface-muted);
  border: 1px solid var(--line);
}
.big {
  font-size: 26px;
  font-weight: 700;
}
.message {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 12px 14px;
  border-radius: var(--radius);
  background: var(--surface-muted);
  border: 1px solid var(--line);
}
.text {
  margin: 0;
  white-space: pre-wrap;
  font-size: 14px;
}
.side {
  gap: 8px;
}
.when {
  font-size: 20px;
  font-weight: 700;
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}
.spacer {
  flex: 1;
}
.pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  padding: 3px 10px;
  border-radius: 999px;
  background: var(--status-cancelled-bg);
  color: var(--status-cancelled-ink);
}
.pill.live {
  background: var(--success-soft);
  color: var(--success-ink);
}
.pulse {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--group-sent);
  animation: pulse 1.8s infinite;
}
@keyframes pulse {
  50% {
    opacity: 0.3;
  }
}
@media (prefers-reduced-motion: reduce) {
  .pulse {
    animation: none;
  }
}
.audience {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px 20px;
  font-size: 14px;
}
.note {
  flex-basis: 100%;
}
.summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px;
}
.stat {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.value {
  font-size: 28px;
  font-weight: 700;
}
.pair {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 13px;
}
.progress {
  height: 6px;
  border-radius: 3px;
  background: var(--line);
  overflow: hidden;
}
.progress div {
  height: 100%;
  background: var(--group-sent);
}
.dot {
  display: inline-block;
  width: 9px;
  height: 9px;
  border-radius: 3px;
  margin-right: 6px;
  flex: 0 0 auto;
}
.dot.sent,
.seg.sent { background: var(--group-sent); }
.dot.failed,
.seg.failed { background: var(--group-failed); }
.dot.skipped,
.seg.skipped { background: var(--group-skipped); }
.dot.retrying,
.seg.retrying { background: var(--group-retrying); }
.dot.cancelled,
.seg.cancelled { background: var(--group-cancelled); }
.dot.pending { background: var(--group-pending); }
.dot.uncertain,
.seg.uncertain {
  background: var(--group-uncertain-soft);
  border: 1.5px dashed var(--group-uncertain);
}
.channels {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(380px, 1fr));
  gap: 16px;
}
.ch-head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 8px;
}
.bar {
  display: flex;
  height: 12px;
  border-radius: 6px;
  overflow: hidden;
  background: var(--group-pending);
}
.seg {
  height: 100%;
}
.rows {
  display: flex;
  flex-direction: column;
}
.channel-row,
a.stat,
a.pair {
  color: inherit;
  text-decoration: none;
}
.channel-row:hover .label,
a.pair:hover,
a.stat:hover .kicker {
  text-decoration: underline;
}
.channel-row {
  display: grid;
  grid-template-columns: 96px minmax(0, 1fr) auto;
  gap: 10px;
  align-items: baseline;
  padding: 7px 0;
  font-size: 14px;
}
.channel-row.first {
  border-top: 1px solid var(--line);
}
.section-name {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--muted);
}
.label {
  display: inline-flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 4px;
}
@media (max-width: 760px) {
  .scheduled,
  .est-grid {
    grid-template-columns: 1fr;
  }
  .channels {
    grid-template-columns: 1fr;
  }
}
</style>
