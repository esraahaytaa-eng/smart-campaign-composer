<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { campaigns } from '../campaign/client'
import { campaignActions, readListQuery, writeListQuery, type Action, type ListPage, type ListQuery, type StatusFilter } from '../campaign/list'
import { briefTitle } from '../campaign/title'
import type { Campaign, Channel } from '../campaign/types'
import CancelDialog from '../components/CancelDialog.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { pageInfo } from '../delivery/recipients'
import { formatDateTime, formatDay, formatNumber, t, type Key } from '../i18n'
import { errorText } from '../i18n/errors'

const FILTERS: StatusFilter[] = ['all', 'draft', 'approved', 'sending', 'completed', 'cancelled']

const route = useRoute()
const router = useRouter()

const data = ref<ListPage | null>(null)
const loadError = ref<unknown>(null)
const actionError = ref<unknown>(null)
const notice = ref<Key | null>(null)
const deleting = ref<Campaign | null>(null)
const cancelling = ref<Campaign | null>(null)

// The filters, search and page live in the address (D54), so a dashboard card
// can open the list filtered, and a reload or Back keeps them.
const query = computed(() => readListQuery(route.query))
let seq = 0 // only the latest request may fill the table
async function load() {
  const mine = ++seq
  try {
    const page = await campaigns.list(query.value)
    if (mine !== seq) return
    // Past the end (e.g. the last row of the last page was deleted): go to the last page.
    if (!page.campaigns.length && page.total > 0 && query.value.page > 1) {
      set({ page: Math.ceil(page.total / (page.page_size ?? 25)) }, false)
      return
    }
    data.value = page
    loadError.value = null
  } catch (err) {
    if (mine === seq) loadError.value = err
  }
}
watch(query, load, { immediate: true })

function set(patch: Partial<ListQuery>, resetPage = true) {
  router.replace({ query: writeListQuery({ ...query.value, ...(resetPage && { page: 1 }), ...patch }) })
}

// The search follows typing after a short pause.
const search = ref(query.value.q)
let timer: ReturnType<typeof setTimeout> | undefined
function onSearch() {
  clearTimeout(timer)
  timer = setTimeout(() => set({ q: search.value.trim() }), 300)
}
onBeforeUnmount(() => clearTimeout(timer))
function clearFilters() {
  clearTimeout(timer)
  search.value = ''
  set({ status: 'all', q: '' })
}

const rows = computed(() => data.value?.campaigns ?? [])
const counts = computed(() => data.value?.counts)
const info = computed(() => (data.value ? pageInfo({ total: data.value.total, page: query.value.page, page_size: data.value.page_size ?? 25 }, rows.value.length) : null))

const channelNames = (chs: Channel[]) => chs.map((ch) => t(`channel.${ch}`)).join(' + ')
const parentTitle = (c: Campaign) => (c.parent_request_text ? briefTitle(c.parent_request_text) : `#${c.parent_campaign_id}`)
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
        <span class="kicker">{{ counts ? t('campaigns.count', { count: counts.all }) : '' }}</span>
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
        <input v-model="search" type="search" :placeholder="t('campaigns.searchPlaceholder')" @input="onSearch" />
      </label>
      <div role="group" class="filters" :aria-label="t('campaigns.filter')">
        <button v-for="f in FILTERS" :key="f" type="button" class="filter" :aria-pressed="query.status === f" @click="set({ status: f })">
          {{ f === 'all' ? t('campaigns.filter.all') : t(`status.${f}`) }} <span v-if="counts" class="n">{{ formatNumber(counts[f]) }}</span>
        </button>
      </div>
    </div>

    <p class="sr-only" role="status">{{ notice ? t(notice) : '' }}</p>
    <div v-if="actionError" role="alert" class="alert alert-error">{{ errorText(actionError) }}</div>
    <div v-if="loadError" role="alert" class="alert alert-error">{{ errorText(loadError) }}</div>
    <p v-else-if="!data" class="loading" role="status">{{ t('list.loading') }}</p>
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
              {{ t('list.resendOf', { title: parentTitle(c) }) }}
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
        <p v-if="!rows.length && data.counts.all" class="empty">
          {{ t('campaigns.noMatch') }} <button type="button" class="link" @click="clearFilters">{{ t('campaigns.clear') }}</button>
        </p>
        <p v-else-if="!data.counts.all" class="empty">
          {{ t('list.empty') }} <RouterLink :to="{ name: 'new-campaign' }" class="link">{{ t('list.createFirst') }}</RouterLink>
        </p>
      </div>
    </div>
    <div v-if="info && info.total" class="foot">
      <span class="note">{{ info.to ? t('campaigns.showing', { from: formatNumber(info.from), to: formatNumber(info.to), total: formatNumber(info.total) }) : '' }}</span>
      <span class="pager">
        <button type="button" class="btn btn-sm" :disabled="!info.hasPrev" @click="set({ page: info.prevPage }, false)">{{ t('pager.prev') }}</button>
        <button type="button" class="btn btn-sm" :disabled="!info.hasNext" @click="set({ page: query.page + 1 }, false)">{{ t('pager.next') }}</button>
      </span>
    </div>
    <p class="note">{{ t('campaigns.note') }}</p>

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
