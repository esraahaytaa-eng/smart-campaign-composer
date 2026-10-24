<script setup lang="ts">
import { computed, ref } from 'vue'
import { campaigns } from '../campaign/client'
import { dashboardAction, dashboardHint, type ListPage } from '../campaign/list'
import { briefTitle } from '../campaign/title'
import type { Campaign, Status } from '../campaign/types'
import StatusBadge from '../components/StatusBadge.vue'
import { formatDateTime, formatLongDate, formatNumber, t } from '../i18n'
import { errorText } from '../i18n/errors'

const STATUSES: Status[] = ['draft', 'approved', 'sending', 'completed', 'cancelled']

// The server counts every campaign and picks the ones needing attention (D54).
const data = ref<ListPage | null>(null)
const loadError = ref<unknown>(null)
campaigns
  .dashboard()
  .then((d) => (data.value = d))
  .catch((err) => (loadError.value = err))

const counts = computed(() => data.value?.counts)
const dash = computed(() => ({ rows: data.value?.campaigns ?? [], more: Math.max(0, (data.value?.total ?? 0) - (data.value?.campaigns.length ?? 0)) }))

function hint(c: Campaign): string {
  const h = dashboardHint(c)
  return 'at' in h ? t(`dashboard.hint.${h.key}`, { time: formatDateTime(h.at) }) : t(`dashboard.hint.${h.key}`)
}
</script>

<template>
  <div class="page">
    <div class="head">
      <div class="titles">
        <span class="kicker">{{ formatLongDate(new Date()) }}</span>
        <h1>{{ t('page.dashboard') }}</h1>
      </div>
      <RouterLink :to="{ name: 'new-campaign' }" class="btn btn-primary btn-lg">{{ t('list.newCampaign') }}</RouterLink>
    </div>

    <div v-if="loadError" role="alert" class="alert alert-error">{{ errorText(loadError) }}</div>
    <p v-else-if="!data || !counts" class="loading" role="status">{{ t('list.loading') }}</p>
    <template v-else>
      <nav class="cards" :aria-label="t('campaigns.filter')">
        <RouterLink v-for="s in STATUSES" :key="s" :to="{ name: 'campaigns', query: { status: s } }" class="card status-card">
          <span class="label" :class="s"><span class="dot" aria-hidden="true" />{{ t(`dashboard.card.${s}`) }}</span>
          <span class="count">{{ formatNumber(counts[s]) }}</span>
          <span class="sub">{{ t(`dashboard.cardSub.${s}`) }}</span>
        </RouterLink>
      </nav>

      <section class="card yours" aria-labelledby="yours-h">
        <div class="yours-head">
          <div class="titles">
            <h2 id="yours-h">{{ t('dashboard.yours') }}</h2>
            <span class="help">{{ t('dashboard.yoursHelp') }}</span>
          </div>
          <RouterLink :to="{ name: 'campaigns' }" class="link">{{ t('dashboard.viewAll') }}</RouterLink>
        </div>
        <div v-for="c in dash.rows" :key="c.id" class="dash-row">
          <span class="name">
            <RouterLink :to="{ name: 'campaign', params: { id: c.id } }" class="title">{{ briefTitle(c.request_text) }}</RouterLink>
            <span class="hint">{{ hint(c) }}</span>
          </span>
          <StatusBadge :status="c.status" />
          <RouterLink
            :to="{ name: 'campaign', params: { id: c.id } }"
            class="btn btn-sm row-action"
            :class="{ 'btn-strong': c.status === 'draft' || c.status === 'sending' }"
            :aria-label="t('list.actionLabel', { action: t(`list.action.${dashboardAction(c)}`), title: briefTitle(c.request_text) })"
            >{{ t(`list.action.${dashboardAction(c)}`) }}</RouterLink
          >
        </div>
        <p v-if="!dash.rows.length" class="empty">
          {{ t('list.empty') }} <RouterLink :to="{ name: 'new-campaign' }" class="link">{{ t('list.createFirst') }}</RouterLink>
        </p>
        <RouterLink v-if="dash.more" :to="{ name: 'campaigns' }" class="link more">{{ t('dashboard.more', { count: dash.more }) }}</RouterLink>
      </section>
    </template>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 20px;
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
.help,
.sub,
.hint {
  font-size: 13px;
  color: var(--muted);
}
.loading {
  color: var(--ink-2);
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 12px;
}
.status-card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 16px;
  color: var(--ink);
  text-decoration: none;
}
.status-card:hover {
  border-color: var(--line-strong);
}
.label {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 13px;
  font-weight: 600;
}
.dot {
  width: 9px;
  height: 9px;
  border-radius: 3px;
}
.label.draft { color: var(--status-draft-ink); }
.label.approved { color: var(--status-approved-ink); }
.label.sending { color: var(--status-sending-ink); }
.label.completed { color: var(--status-completed-ink); }
.label.cancelled { color: var(--status-cancelled-ink); }
.draft .dot { background: var(--status-draft-dot); }
.approved .dot { background: var(--status-approved-dot); }
.sending .dot { background: var(--status-sending-dot); }
.completed .dot { background: var(--status-completed-dot); }
.cancelled .dot { background: var(--status-cancelled-dot); }
.count {
  font-size: 28px;
  font-weight: 700;
}
.yours {
  display: flex;
  flex-direction: column;
  padding: 8px 20px 12px;
}
.yours-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 12px 0;
}
h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 650;
}
.link {
  color: var(--link);
  font-weight: 600;
  font-size: 14px;
  text-decoration: none;
}
.link:hover {
  text-decoration: underline;
}
.dash-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto 112px;
  gap: 16px;
  align-items: center;
  padding: 14px 0;
  border-top: 1px solid var(--line);
}
.name {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.title {
  font-size: 15px;
  font-weight: 600;
  color: var(--ink);
  text-decoration: none;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.row-action {
  justify-self: end;
  width: 104px;
  justify-content: center;
}
.btn-strong {
  background: var(--ink);
  border-color: var(--ink);
  color: var(--surface);
}
.empty {
  margin: 0;
  padding: 28px 0;
  border-top: 1px solid var(--line);
  text-align: center;
  color: var(--ink-2);
}
.more {
  padding: 12px 0 4px;
  border-top: 1px solid var(--line);
  font-size: 13px;
}
@media (max-width: 640px) {
  .dash-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .row-action {
    grid-column: 1 / -1;
    justify-self: start;
  }
}
</style>
