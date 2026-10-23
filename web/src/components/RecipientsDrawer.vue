<script setup lang="ts">
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { campaigns } from '../campaign/client'
import { briefTitle } from '../campaign/title'
import type { Campaign, Channel } from '../campaign/types'
import {
  attemptView,
  MAX_QUERY,
  pageInfo,
  readQuery,
  rowDetail,
  tabCounts,
  writeQuery,
  type RecipientsPage,
  type RecipientsQuery,
} from '../delivery/recipients'
import type { Group, ResendOffer } from '../delivery/status'
import { formatDateTime, formatNumber, formatTime, isKey, locale, t } from '../i18n'
import { errorText } from '../i18n/errors'

// Recipient details (board 5, D47): a side dialog whose filters live in the
// URL query. It shows a snapshot taken when it opens or a filter changes.
const props = defineProps<{ campaign: Campaign; offers: Partial<Record<Channel, ResendOffer>> }>()
const emit = defineEmits<{ resend: [Channel] }>()

const route = useRoute()
const router = useRouter()
const query = computed(() => readQuery(route.query))
const data = ref<RecipientsPage | null>(null)
const loadError = ref<unknown>(null)
const loading = ref(false)
const openRow = ref<number | null>(null)
const search = ref('')
const searchInput = ref<HTMLInputElement>()

let seq = 0 // only the latest request may fill the table
let timer: ReturnType<typeof setTimeout> | undefined
async function load(q: RecipientsQuery) {
  const mine = ++seq
  loading.value = true
  loadError.value = null
  try {
    const page = await campaigns.recipients(props.campaign.id, q)
    if (mine === seq) data.value = page
  } catch (err) {
    if (mine === seq) loadError.value = err
  } finally {
    if (mine === seq) loading.value = false
  }
}
watch(
  query,
  (q, old) => {
    if (!q) {
      seq++ // a request still on its way must not fill the closed drawer
      clearTimeout(timer) // nor a pending search reopen it (Back while typing)
      data.value = null
      return
    }
    // Keep what is being typed: the address can lag behind the input.
    if (q.q !== search.value.trim() && document.activeElement !== searchInput.value) search.value = q.q
    if (JSON.stringify(q) !== JSON.stringify(old)) {
      openRow.value = null
      // Another tab or channel: show nothing rather than the last one's rows.
      if (old && (q.group !== old.group || q.channel !== old.channel)) data.value = null
      load(q)
    }
  },
  { immediate: true },
)

function set(patch: Partial<RecipientsQuery>) {
  if (!query.value) return
  const next = { ...query.value, page: 1, ...patch }
  router.replace({ query: writeQuery(next) })
}

// The name search follows typing after a short pause, and at once on Enter.
function onSearch() {
  clearTimeout(timer)
  timer = setTimeout(() => set({ q: search.value.trim() }), 300)
}
function searchNow() {
  clearTimeout(timer)
  set({ q: search.value.trim() })
}
onBeforeUnmount(() => clearTimeout(timer))

// Opening pushed a history entry, so closing goes back to it; a link that
// opened the drawer directly has nothing to go back to and replaces instead.
function close() {
  clearTimeout(timer)
  if (window.history.state?.back === route.path) router.back()
  else router.replace({ query: {} })
}

const tabs = computed(() => (data.value && query.value ? tabCounts(data.value.groups, query.value.channel) : []))
const total = computed(() =>
  Object.values(data.value?.groups ?? {}).reduce((n, g) => n + Object.values(g).reduce((a, b) => a + b, 0), 0),
)
const groupTotal = computed(() => tabs.value.find((x) => x.group === query.value?.group)?.count ?? 0)
const info = computed(() => (data.value ? pageInfo(data.value, data.value.recipients.length) : null))
const channels = computed<(Channel | 'all')[]>(() => (props.campaign.channels.length > 1 ? ['all', ...props.campaign.channels] : []))

// The reason box: each reason with its count and what it means; groups
// without reasons get one line for the whole group.
const reasons = computed(() => {
  const g = query.value?.group
  if (!data.value || !g) return []
  const entries = Object.entries(data.value.reasons).sort((a, b) => b[1] - a[1])
  if (entries.length) return entries.map(([code, count]) => ({ name: label('reasonName', code), count, desc: label('infoDesc', code) }))
  return [{ name: label('infoName', g), count: groupTotal.value, desc: label('infoDesc', g) }]
})
function label(prefix: string, code: string) {
  const key = `${prefix}.${code}`
  return isKey(key) ? t(key) : code
}
const groupLabel = (g: Group) => t(`group.${g}`)
function detailText(d: ReturnType<typeof rowDetail>) {
  if ('at' in d) return t(d.key, { time: formatTime(new Date(d.at)) })
  if ('length' in d) return t(d.key, { length: formatNumber(d.length), limit: formatNumber(d.limit) })
  if ('count' in d) return t(d.key, { count: d.count })
  return t(d.key)
}
const last = (r: RecipientsPage['recipients'][number]) => r.attempts.at(-1)?.started_at

// On the Skipped tab, the way to a resend for the channel shown (D48).
const resendChannel = computed<Channel | null>(() => {
  const q = query.value
  if (q?.group !== 'skipped') return null
  const chs = q.channel === 'all' ? props.campaign.channels : [q.channel]
  return chs.find((ch) => props.offers[ch]?.state === 'available') ?? null
})
function resend(ch: Channel) {
  close()
  emit('resend', ch)
}
</script>

<template>
  <DialogRoot :open="query !== null" @update:open="(v) => !v && close()">
    <DialogPortal>
      <DialogOverlay class="overlay" />
      <DialogContent class="drawer">
        <div class="head">
          <div class="titles">
            <span v-if="data" class="meta">
              {{ t('recipients.meta', { campaign: briefTitle(campaign.request_text), count: total, n: formatNumber(total), time: formatTime(new Date(data.as_of)) }) }}
            </span>
            <DialogTitle class="title">{{ t('recipients.title') }}</DialogTitle>
            <DialogDescription class="sr-only">{{ t('recipients.description', { campaign: briefTitle(campaign.request_text) }) }}</DialogDescription>
          </div>
          <DialogClose class="close" :aria-label="t('recipients.close')">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" /></svg>
          </DialogClose>
        </div>

        <template v-if="query">
          <div role="tablist" class="tabs" :aria-label="t('recipients.status')">
            <button
              v-for="tab in tabs"
              :key="tab.group"
              type="button"
              role="tab"
              class="tab"
              :aria-selected="tab.group === query.group"
              @click="set({ group: tab.group })"
            >
              <span :class="['dot', tab.group]" aria-hidden="true" />{{ groupLabel(tab.group) }} <span class="n">{{ formatNumber(tab.count) }}</span>
            </button>
          </div>

          <div class="tools">
            <label class="search">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                <circle cx="11" cy="11" r="7" />
                <path d="M20 20l-4-4" />
              </svg>
              <span class="sr-only">{{ t('recipients.search') }}</span>
              <input ref="searchInput" v-model="search" type="search" :maxlength="MAX_QUERY" :placeholder="t('recipients.searchPlaceholder')" @input="onSearch" @keydown.enter.prevent="searchNow" />
            </label>
            <div v-if="channels.length" role="group" class="segmented" :aria-label="t('recipients.channel')">
              <button v-for="ch in channels" :key="ch" type="button" :aria-pressed="query.channel === ch" @click="set({ channel: ch })">
                {{ ch === 'all' ? t('recipients.allChannels') : t(`channel.${ch}`) }}
              </button>
            </div>
          </div>

          <div v-if="loadError" role="alert" class="alert alert-error">{{ errorText(loadError) }}</div>
          <p v-else-if="!data" class="muted" role="status">{{ t('recipients.loading') }}</p>
          <template v-else>
            <div :class="['info', query.group]">
              <span class="info-title">{{ t(`info.${query.group}`) }}</span>
              <div v-for="r in reasons" :key="r.name" class="reason">
                <strong>{{ r.name }} · {{ formatNumber(r.count) }}</strong>
                <span>{{ r.desc }}</span>
              </div>
            </div>

            <div class="table-wrap" :aria-busy="loading">
              <div role="table" class="table" :aria-label="t('recipients.table')">
                <div role="row" class="row header">
                  <span role="columnheader">{{ t('recipients.col.recipient') }}</span>
                  <span role="columnheader">{{ t('recipients.col.channel') }}</span>
                  <span role="columnheader">{{ t('recipients.col.status') }}</span>
                  <span role="columnheader">{{ t('recipients.col.reason') }}</span>
                  <span role="columnheader">{{ t('recipients.col.attempts') }}</span>
                  <span role="columnheader">{{ t('recipients.col.last') }}</span>
                  <span role="columnheader"><span class="sr-only">{{ t('recipients.col.history') }}</span></span>
                </div>
                <template v-for="r in data.recipients" :key="r.id">
                  <div role="row" class="row" :class="{ open: openRow === r.id }">
                    <span role="cell" class="who"><strong class="name">{{ r.name }}</strong><span class="contact">{{ r.contact }}</span></span>
                    <span role="cell">{{ t(`channel.${r.channel}`) }}</span>
                    <span role="cell"><span :class="['badge', r.group]">{{ groupLabel(r.group) }}</span></span>
                    <span role="cell" class="why">
                      <span>{{ r.reason ? label('reasonName', r.reason) : label('infoName', r.group) }}</span>
                      <span class="muted">{{ detailText(rowDetail(r)) }}</span>
                    </span>
                    <span role="cell" class="num">{{ r.attempts.length }}</span>
                    <span role="cell" class="mono">{{ last(r) ? formatTime(new Date(last(r)!)) : '—' }}</span>
                    <span role="cell">
                      <button
                        v-if="r.attempts.length"
                        type="button"
                        class="expand"
                        :aria-expanded="openRow === r.id"
                        :aria-label="t('recipients.showHistory', { name: r.name })"
                        @click="openRow = openRow === r.id ? null : r.id"
                      >
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M6 9l6 6 6-6" /></svg>
                      </button>
                    </span>
                  </div>
                  <div v-if="openRow === r.id" class="history">
                    <div v-for="(a, i) in r.attempts" :key="a.attempt_no" class="attempt">
                      <strong>{{ t('recipients.attempt', { n: a.attempt_no }) }}</strong>
                      <span class="mono muted" :title="formatDateTime(a.started_at)">{{ formatTime(new Date(a.started_at)) }}</span>
                      <template v-for="v in [attemptView(a, i === r.attempts.length - 1, r)]" :key="v.outcome">
                        <span v-if="v.code" :class="['mono', 'code', v.tone]">{{ v.code }}</span>
                        <span :class="['outcome', v.tone]">{{ label('outcome', v.outcome) }} → {{ t(`next.${v.next}`) }}</span>
                      </template>
                    </div>
                  </div>
                </template>
                <p v-if="!data.recipients.length" class="empty">
                  {{ query.q ? t('recipients.noMatch', { q: query.q }) : t('recipients.noMessages', { group: groupLabel(query.group).toLocaleLowerCase(locale) }) }}
                </p>
              </div>
            </div>

            <div class="foot">
              <span class="muted">
                {{ info && info.to ? t('recipients.showing', { from: formatNumber(info.from), to: formatNumber(info.to), total: formatNumber(info.total) }) : t('recipients.showingNone', { total: formatNumber(data.total) }) }}
              </span>
              <button v-if="resendChannel" type="button" class="btn btn-sm btn-primary" @click="resend(resendChannel)">
                {{ t('resend.drawerLink', { channel: t(`channel.inline.${resendChannel}`) }) }}
              </button>
              <span class="pager">
                <button type="button" class="btn btn-sm" :disabled="!info?.hasPrev" @click="set({ page: info!.prevPage })">{{ t('recipients.prev') }}</button>
                <button type="button" class="btn btn-sm" :disabled="!info?.hasNext" @click="set({ page: query.page + 1 })">{{ t('recipients.next') }}</button>
              </span>
            </div>
          </template>
        </template>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style scoped>
.drawer {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: 51;
  width: min(1000px, 100%);
  overflow-y: auto;
  background: var(--surface);
  box-shadow: var(--drawer-shadow);
  padding: 26px 28px 32px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.drawer:focus {
  outline: none;
}
@media (max-width: 640px) {
  .drawer {
    padding: 20px 16px;
  }
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
}
.titles {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.meta,
.muted {
  font-size: 12px;
  color: var(--muted);
}
.title {
  margin: 0;
  font-size: 22px;
  font-weight: 700;
}
.close {
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  border-radius: var(--radius);
  border: 1px solid var(--line);
  background: var(--surface);
  color: var(--ink);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}
.tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.tab {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 34px;
  padding: 0 12px;
  border-radius: 999px;
  border: 1px solid var(--line-strong);
  background: var(--surface);
  color: var(--ink-2);
  font: 600 13px var(--font);
  cursor: pointer;
}
.tab[aria-selected='true'] {
  background: var(--ink);
  border-color: var(--ink);
  color: var(--surface);
}
.n {
  font-weight: 500;
  opacity: 0.85;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 2px;
}
.dot.sent { background: var(--group-sent); }
.dot.failed { background: var(--group-failed); }
.dot.skipped { background: var(--group-skipped); }
.dot.retrying { background: var(--group-retrying); }
.dot.pending { background: var(--group-pending); }
.dot.cancelled { background: var(--group-cancelled); }
.dot.uncertain {
  background: var(--group-uncertain-soft);
  border: 1.5px dashed var(--group-uncertain);
}
.tools {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
  justify-content: space-between;
}
.search {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1 1 260px;
  max-width: 360px;
  height: 38px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  padding: 0 10px;
  color: var(--muted);
}
.search:focus-within {
  outline: var(--focus-ring);
  outline-offset: 1px;
}
.search input {
  flex: 1;
  min-width: 0;
  border: 0;
  outline: none;
  font: 400 14px var(--font);
  color: var(--ink);
  background: transparent;
}
.segmented {
  display: inline-flex;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  overflow: hidden;
}
.segmented button {
  height: 36px;
  padding: 0 14px;
  border: 0;
  background: var(--surface);
  color: var(--ink-2);
  font: 600 13px var(--font);
  cursor: pointer;
}
.segmented button + button {
  border-left: 1px solid var(--line-strong);
}
.segmented button[aria-pressed='true'] {
  background: var(--ink);
  color: var(--surface);
}
.info {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px 16px;
  border-radius: var(--radius-lg);
  background: var(--surface-muted);
  border: 1px solid var(--line);
}
.info.failed { background: var(--danger-faint); border-color: var(--danger-soft-line); }
.info.skipped { background: var(--notice-faint); border-color: var(--notice-line); }
.info.uncertain { background: var(--group-uncertain-faint); border: 1px dashed var(--group-uncertain-line); }
.info.sent { background: var(--success-faint); border-color: var(--success-line); }
.info-title {
  font-size: 13px;
  font-weight: 700;
}
.info.failed .info-title { color: var(--danger); }
.info.skipped .info-title { color: var(--notice-ink); }
.info.uncertain .info-title { color: var(--group-uncertain-ink); }
.info.sent .info-title { color: var(--success-ink); }
.info.retrying .info-title { color: var(--status-sending-ink); }
.reason {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  align-items: baseline;
  font-size: 13px;
  line-height: 1.5;
}
.reason strong {
  font-weight: 600;
}
.reason span {
  flex: 1 1 280px;
  color: var(--ink-2);
}
.table-wrap {
  overflow-x: auto;
  border: 1px solid var(--line);
  border-radius: var(--radius-lg);
}
.table-wrap[aria-busy='true'] {
  opacity: 0.6;
}
.table {
  min-width: 780px;
  font-size: 13px;
}
.row {
  display: grid;
  grid-template-columns: minmax(0, 1.8fr) 70px 110px minmax(0, 2fr) 76px 100px 40px;
  gap: 12px;
  align-items: center;
  padding: 10px 14px;
  border-top: 1px solid var(--line);
}
.row.open {
  background: var(--surface-muted);
}
.header {
  border-top: 0;
  background: var(--surface-muted);
  color: var(--ink-2);
  font-size: 12px;
  font-weight: 600;
}
.who,
.why {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.name {
  font-weight: 600;
}
.contact,
.mono {
  font-family: var(--mono);
  font-size: 12px;
}
.contact {
  color: var(--muted);
}
.num {
  font-variant-numeric: tabular-nums;
}
.badge {
  font-size: 12px;
  font-weight: 600;
  padding: 3px 8px;
  border-radius: 6px;
  white-space: nowrap;
}
.badge.sent { background: var(--success-soft); color: var(--success-ink); }
.badge.failed { background: var(--danger-soft); color: var(--danger); }
.badge.skipped { background: var(--notice-soft); color: var(--notice-ink); }
.badge.uncertain { background: var(--group-uncertain-soft); color: var(--group-uncertain-ink); border: 1px dashed var(--group-uncertain-line); }
.badge.retrying { background: var(--status-sending-bg); color: var(--status-sending-ink); }
.badge.pending { background: var(--group-pending-bg); color: var(--group-pending-ink); }
.badge.cancelled { background: var(--status-cancelled-bg); color: var(--status-cancelled-ink); }
.expand {
  width: 32px;
  height: 32px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--line);
  background: var(--surface);
  color: var(--ink-2);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}
.expand[aria-expanded='true'] svg {
  transform: rotate(180deg);
}
.history {
  background: var(--surface-muted);
  padding: 2px 14px 14px 28px;
}
.attempt {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  align-items: baseline;
  padding: 6px 0 6px 16px;
  border-left: 2px solid var(--line);
  font-size: 12px;
}
.attempt strong {
  width: 80px;
}
.code {
  font-weight: 600;
  min-width: 70px;
}
.ok { color: var(--success-ink); }
.retry { color: var(--status-sending-ink); }
.unknown { color: var(--group-uncertain-ink); }
.fail { color: var(--danger); }
.outcome.retry,
.outcome.ok,
.outcome.fail,
.outcome.unknown {
  color: var(--ink-2);
}
.empty {
  margin: 0;
  padding: 28px;
  text-align: center;
  color: var(--muted);
  border-top: 1px solid var(--line);
}
.foot {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}
.pager {
  display: flex;
  gap: 6px;
}
</style>
