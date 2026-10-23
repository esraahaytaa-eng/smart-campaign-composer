<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { campaigns } from '../campaign/client'
import { campaignActions, filterCampaigns, statusCounts, type Action, type StatusFilter } from '../campaign/list'
import { briefTitle } from '../campaign/title'
import type { Campaign, Channel } from '../campaign/types'
import CancelDialog from '../components/CancelDialog.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { formatDateTime, formatDay, formatNumber, t, type Key } from '../i18n'
import { errorText } from '../i18n/errors'

const LIST_LIMIT = 200 // what GET /api/campaigns returns (D49)
const FILTERS: StatusFilter[] = ['all', 'draft', 'approved', 'sending', 'completed', 'cancelled']

const route = useRoute()
const router = useRouter()

const list = ref<Campaign[] | null>(null)
const loadError = ref<unknown>(null)
const actionError = ref<unknown>(null)
const notice = ref<Key | null>(null)
const deleting = ref<Campaign | null>(null)
const cancelling = ref<Campaign | null>(null)

async function load() {
  try {
    list.value = await campaigns.list()
    loadError.value = null
  } catch (err) {
    loadError.value = err
  }
}
load()

// Filter and search live in the URL, so a dashboard card can open the list
// filtered, and a reload or Back keeps them.
const status = computed<StatusFilter>(() => {
  const s = String(route.query.status ?? 'all')
  return (FILTERS as string[]).includes(s) ? (s as StatusFilter) : 'all'
})
const query = computed(() => String(route.query.q ?? ''))
function setFilters(next: { status?: StatusFilter; q?: string }) {
  const s = next.status ?? status.value
  const q = next.q ?? query.value
  router.replace({ query: { ...(s !== 'all' && { status: s }), ...(q && { q }) } })
}

const counts = computed(() => statusCounts(list.value ?? []))
const rows = computed(() => filterCampaigns(list.value ?? [], status.value, query.value))
const byId = computed(() => new Map((list.value ?? []).map((c) => [c.id, c])))

const channelNames = (chs: Channel[]) => chs.map((ch) => t(`channel.${ch}`)).join(' + ')
const parentTitle = (id: number) => {
  const p = byId.value.get(id)
  return p ? briefTitle(p.request_text) : `#${id}`
}
const linked: Partial<Record<Action, true>> = { edit: true, view: true, monitor: true, viewResults: true }

function act(action: Action, c: Campaign) {
  actionError.value = null
  notice.value = null
  if (action === 'delete') deleting.value = c
  else if (action === 'cancel') cancelling.value = c
}

async function remove() {
  const c = deleting.value!
  deleting.value = null
  try {
    await campaigns.remove(c.id, c.version)
    notice.value = 'campaigns.deleted'
  } catch (err) {
    actionError.value = err
  }
  await load()
}

async function cancel(c: Campaign) {
  cancelling.value = null
  try {
    await campaigns.cancel(c.id)
    notice.value = 'campaigns.cancelledNotice'
  } catch (err) {
    actionError.value = err
  }
  await load()
}
</script>

<template>
  <div class="page">
    <div class="head">
      <div class="titles">
        <span class="kicker">{{ t('campaigns.count', { count: counts.all }) }}</span>
        <h1>{{ t('page.campaigns') }}</h1>
      </div>
      <RouterLink :to="{ name: 'new-campaign' }" class="btn btn-primary btn-lg">{{ t('list.newCampaign') }}</RouterLink>
    </div>

    <div class="tools">
      <label class="search">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
          <circle cx="11" cy="11" r="7" />
          <path d="M20 20l-4-4" />
        </svg>
        <span class="sr-only">{{ t('campaigns.search') }}</span>
        <input type="search" :value="query" :placeholder="t('campaigns.searchPlaceholder')" @input="setFilters({ q: ($event.target as HTMLInputElement).value })" />
      </label>
      <div role="group" class="filters" :aria-label="t('campaigns.filter')">
        <button v-for="f in FILTERS" :key="f" type="button" class="filter" :aria-pressed="status === f" @click="setFilters({ status: f })">
          {{ f === 'all' ? t('campaigns.filter.all') : t(`status.${f}`) }} <span class="n">{{ formatNumber(counts[f]) }}</span>
        </button>
      </div>
    </div>

    <p class="sr-only" role="status">{{ notice ? t(notice) : '' }}</p>
    <div v-if="actionError" role="alert" class="alert alert-error">{{ errorText(actionError) }}</div>
    <div v-if="loadError" role="alert" class="alert alert-error">{{ errorText(loadError) }}</div>
    <p v-else-if="!list" class="loading" role="status">{{ t('list.loading') }}</p>
    <div v-else class="card table-wrap">
      <div role="table" class="table" :aria-label="t('page.campaigns')">
        <div role="row" class="row header">
          <span role="columnheader">{{ t('campaigns.col.campaign') }}</span>
          <span role="columnheader">{{ t('campaigns.col.channels') }}</span>
          <span role="columnheader">{{ t('campaigns.col.status') }}</span>
          <span role="columnheader">{{ t('campaigns.col.created') }}</span>
          <span role="columnheader">{{ t('campaigns.col.scheduled') }}</span>
          <span role="columnheader" class="end">{{ t('campaigns.col.actions') }}</span>
        </div>
        <div v-for="c in rows" :key="c.id" role="row" class="row">
          <span role="cell" class="name">
            <RouterLink :to="{ name: 'campaign', params: { id: c.id } }" class="title">{{ briefTitle(c.request_text) }}</RouterLink>
            <RouterLink v-if="c.parent_campaign_id" :to="{ name: 'campaign', params: { id: c.parent_campaign_id } }" class="parent">
              {{ t('list.resendOf', { title: parentTitle(c.parent_campaign_id) }) }}
            </RouterLink>
          </span>
          <span role="cell">{{ channelNames(c.channels) }}</span>
          <span role="cell"><StatusBadge :status="c.status" /></span>
          <span role="cell" class="muted">{{ formatDay(c.created_at) }}</span>
          <span role="cell" class="muted">{{ c.status !== 'draft' && c.send_at ? formatDateTime(c.send_at) : '—' }}</span>
          <span role="cell" class="actions">
            <template v-for="a in campaignActions(c)" :key="a">
              <RouterLink
                v-if="linked[a]"
                :to="{ name: 'campaign', params: { id: c.id } }"
                class="btn btn-sm"
                :class="{ 'btn-strong': a !== 'view' }"
                :aria-label="t('list.actionLabel', { action: t(`list.action.${a}`), title: briefTitle(c.request_text) })"
                >{{ t(`list.action.${a}`) }}</RouterLink
              >
              <button
                v-else
                type="button"
                class="btn btn-sm btn-danger-outline"
                :aria-label="t('list.actionLabel', { action: t(`list.action.${a}`), title: briefTitle(c.request_text) })"
                @click="act(a, c)"
              >
                {{ t(`list.action.${a}`) }}
              </button>
            </template>
          </span>
        </div>
        <p v-if="!rows.length && list.length" class="empty">
          {{ t('campaigns.noMatch') }} <button type="button" class="link" @click="setFilters({ status: 'all', q: '' })">{{ t('campaigns.clear') }}</button>
        </p>
        <p v-else-if="!list.length" class="empty">
          {{ t('list.empty') }} <RouterLink :to="{ name: 'new-campaign' }" class="link">{{ t('list.createFirst') }}</RouterLink>
        </p>
      </div>
    </div>
    <p class="note">
      {{ t('campaigns.note') }}
      <template v-if="list && list.length >= LIST_LIMIT">{{ t('campaigns.limit', { count: LIST_LIMIT }) }}</template>
    </p>

    <ConfirmDialog
      :open="deleting !== null"
      :title="t('confirm.delete')"
      :confirm-label="t('confirm.deleteAction')"
      :cancel-label="t('confirm.deleteKeep')"
      danger
      @confirm="remove"
      @close="deleting = null"
    >
      <p><strong>{{ deleting ? briefTitle(deleting.request_text) : '' }}</strong></p>
      <p>{{ t('confirm.deleteBody') }}</p>
    </ConfirmDialog>
    <CancelDialog :campaign="cancelling" @confirm="cancel" @close="cancelling = null" />
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px;
}
.titles {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.kicker,
.note,
.muted {
  font-size: 13px;
  color: var(--muted);
}
.muted {
  color: var(--ink-2);
  white-space: nowrap;
}
.note {
  margin: 0;
  font-size: 12px;
}
.loading {
  color: var(--ink-2);
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
  max-width: 380px;
  height: 40px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  padding: 0 12px;
  background: var(--surface);
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
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.filter {
  height: 34px;
  padding: 0 12px;
  border-radius: 999px;
  border: 1px solid var(--line-strong);
  background: var(--surface);
  color: var(--ink-2);
  font: 600 13px var(--font);
  cursor: pointer;
}
.filter[aria-pressed='true'] {
  background: var(--ink);
  border-color: var(--ink);
  color: var(--surface);
}
.n {
  font-weight: 500;
  opacity: 0.8;
}
.table-wrap {
  padding: 0;
  overflow-x: auto;
}
.table {
  min-width: 900px;
  font-size: 13px;
}
.row {
  display: grid;
  grid-template-columns: minmax(0, 2.4fr) 110px 130px 110px 170px 220px;
  gap: 12px;
  align-items: center;
  padding: 12px 16px;
  border-top: 1px solid var(--line);
}
.header {
  border-top: 0;
  background: var(--surface-muted);
  color: var(--ink-2);
  font-size: 12px;
  font-weight: 600;
}
.end {
  text-align: right;
}
.name {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.title {
  font-size: 14px;
  font-weight: 600;
  color: var(--ink);
  text-decoration: none;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.title:hover,
.parent:hover,
.link:hover {
  text-decoration: underline;
}
.parent {
  font-size: 12px;
  color: var(--link);
  text-decoration: none;
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 6px;
}
.btn-strong {
  background: var(--ink);
  border-color: var(--ink);
  color: var(--surface);
}
.empty {
  margin: 0;
  padding: 40px;
  text-align: center;
  color: var(--muted);
  border-top: 1px solid var(--line);
}
.link {
  border: 0;
  background: none;
  padding: 0;
  color: var(--link);
  font: 600 13px var(--font);
  text-decoration: none;
  cursor: pointer;
}
</style>
