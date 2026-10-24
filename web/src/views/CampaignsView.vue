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
    const last = Math.ceil(page.total / (page.page_size ?? 25))
    if (!page.campaigns.length && page.total > 0 && query.value.page > last) {
      set({ page: last }, false)
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
// The address can change the search too (the sidebar link, Back): the box follows.
watch(
  () => query.value.q,
  (q) => {
    if (q === search.value.trim()) return
    clearTimeout(timer)
    search.value = q
  },
)
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
        <input v-model="search" type="search" maxlength="50" :placeholder="t('campaigns.searchPlaceholder')" @input="onSearch" />
      </label>
      <div role="group" class="filters" :aria-label="t('campaigns.filter')">
        <button v-for="f in FILTERS" :key="f" type="button" class="filter" :aria-pressed="query.status === f" @click="set({ status: f })">
          {{ f === 'all' ? t('campaigns.filter.all') : t(`status.${f}`) }} <span v-if="counts" class="n">{{ formatNumber(counts[f]) }}</span>
        </button>
      </div>
    </div>

    <!-- Also above the rows: 25 of them push the lower pager below the fold (D62). -->
    <nav v-if="info && (info.hasPrev || info.hasNext)" class="pager pager-top" :aria-label="t('pager.above')">
      <span class="note">{{ info.to ? t('campaigns.range', { from: formatNumber(info.from), to: formatNumber(info.to), total: formatNumber(info.total) }) : '' }}</span>
      <button type="button" class="btn btn-sm" :disabled="!info.hasPrev" @click="set({ page: info.prevPage }, false)">{{ t('pager.prev') }}</button>
      <button type="button" class="btn btn-sm" :disabled="!info.hasNext" @click="set({ page: query.page + 1 }, false)">{{ t('pager.next') }}</button>
    </nav>

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
          <span role="cell" :data-label="t('campaigns.col.channels')">{{ channelNames(c.channels) }}</span>
          <span role="cell" :data-label="t('campaigns.col.status')"><StatusBadge :status="c.status" /></span>
          <span role="cell" class="muted" :data-label="t('campaigns.col.created')">{{ formatDay(c.created_at) }}</span>
          <span role="cell" class="muted" :data-label="t('campaigns.col.scheduled')">{{ c.status !== 'draft' && c.send_at ? formatDateTime(c.send_at) : '—' }}</span>
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
      <nav class="pager" :aria-label="t('pager.below')">
        <button type="button" class="btn btn-sm" :disabled="!info.hasPrev" @click="set({ page: info.prevPage }, false)">{{ t('pager.prev') }}</button>
        <button type="button" class="btn btn-sm" :disabled="!info.hasNext" @click="set({ page: query.page + 1 }, false)">{{ t('pager.next') }}</button>
      </nav>
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
  align-items: center;
  gap: 6px;
}
.pager-top {
  align-self: flex-end;
  margin: -6px 0;
}
.pager-top .note {
  margin-right: 6px;
}
.table-wrap {
  padding: 0;
  container-type: inline-size;
}
.table {
  font-size: 13px;
}
.row {
  display: grid;
  /* Sized to the longest English or Turkish value (two action buttons); the title takes the rest. */
  grid-template-columns: minmax(0, 1fr) 100px 110px 95px 165px 170px;
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
/* A narrow list (tablet, phone): each campaign is a card, labelled values below its title. */
@container (max-width: 860px) {
  .row {
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 10px 16px;
    padding: 14px 16px;
  }
  .header {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
  .header + .row {
    border-top: 0;
  }
  .name,
  .actions {
    grid-column: 1 / -1;
  }
  .title {
    white-space: normal;
  }
  .actions {
    justify-content: flex-start;
  }
  [data-label] {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 2px;
    white-space: normal;
  }
  [data-label]::before {
    content: attr(data-label);
    font-size: 11px;
    font-weight: 600;
    color: var(--muted);
  }
}
</style>
