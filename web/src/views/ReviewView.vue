<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { campaigns, type Estimate } from '../campaign/client'
import { ruleLabel } from '../campaign/rules'
import { briefTitle } from '../campaign/title'
import type { Campaign, Channel } from '../campaign/types'
import { approvalBlockers, type Blocker } from '../content/approval'
import { SAMPLES } from '../content/preview'
import { contentWasStale } from '../content/stale'
import { browserZone, fromLocalInput, scheduleProblem, suggestion, toLocalInput } from '../content/schedule'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import MessagePreview from '../components/MessagePreview.vue'
import StepBar from '../components/StepBar.vue'
import { formatDateTime, formatNumber, t } from '../i18n'
import { errorText } from '../i18n/errors'
import { ApiError } from '../api'

const route = useRoute()
const router = useRouter()
const id = Number(route.params.id)
// The brief was re-interpreted after the messages were written (D40).
const stale = contentWasStale(id)

const campaign = ref<Campaign | null>(null)
const estimate = ref<Estimate | null>(null)
const loadError = ref<unknown>(null)
const approveError = ref<unknown>(null)

// The send choice. Nothing is chosen unless the brief's time is still usable
// (then it is labelled as a suggestion): sending is always explicit (D29).
const when = ref<'now' | 'later' | null>(null)
const localAt = ref('')
const note = ref<string | null>(null) // the brief's own words, shown as written
const suggested = ref(false)
const reviewed = ref(false)
const tried = ref(false)
const confirmOpen = ref(false)
const busy = ref(false)
// The clock the schedule checks use: it ticks, and is refreshed on every
// click, so a time that goes stale while the page is open is caught (review 5).
const now = ref(new Date())
const tick = setInterval(() => (now.value = new Date()), 15_000)
onBeforeUnmount(() => clearInterval(tick))
const conflict = computed(() => approveError.value instanceof ApiError && approveError.value.status === 409)

async function load() {
  try {
    const c = await campaigns.get(id)
    campaign.value = c
    const s = suggestion(c.send_at, c.schedule_note, new Date())
    note.value = s.note
    suggested.value = s.preselect
    if (s.preselect && s.at) {
      when.value = 'later'
      localAt.value = toLocalInput(s.at)
    }
    // Without the estimate the page still works: the counts fall back to the draft's.
    estimate.value = await campaigns.estimate(id).catch(() => null)
  } catch (err) {
    loadError.value = err
  }
}
load()

const channels = computed<Channel[]>(() => campaign.value?.channels ?? [])
const at = computed(() => fromLocalInput(localAt.value))
const counts = computed(() => estimate.value?.audience ?? campaign.value?.counts ?? { total: 0, sms: 0, email: 0 })
const over = computed(() => estimate.value?.over_limit ?? null)
const messages = computed(() => channels.value.reduce((n, c) => n + (counts.value[c] ?? 0), 0))
const blockers = computed<Blocker[]>(() =>
  campaign.value
    ? approvalBlockers({
        channels: channels.value,
        content: campaign.value.content,
        audience: counts.value.total,
        when: when.value,
        at: at.value,
        reviewed: reviewed.value,
        scheduleProblem: at.value ? scheduleProblem(at.value, now.value) : null,
      })
    : [],
)
const sample = SAMPLES[0]!.vars
const rules = computed(() => (campaign.value?.rules ?? []).map(ruleLabel))
const isResend = computed(() => campaign.value?.parent_campaign_id != null)
const blockerList = ref<HTMLElement>()

async function openConfirm() {
  now.value = new Date()
  tried.value = true
  if (blockers.value.length) {
    await nextTick()
    blockerList.value?.focus()
    return
  }
  confirmOpen.value = true
}

async function approve() {
  confirmOpen.value = false
  now.value = new Date()
  if (blockers.value.length) {
    // e.g. the time went stale while the dialog was open: say so, like openConfirm
    tried.value = true
    await nextTick()
    blockerList.value?.focus()
    return
  }
  if (!campaign.value || busy.value) return
  busy.value = true
  approveError.value = null
  try {
    const send = when.value === 'now' ? 'now' : { at: at.value! }
    await campaigns.approve(id, campaign.value.version, send)
    await router.replace({ name: 'campaign', params: { id } })
  } catch (err) {
    approveError.value = err
  } finally {
    busy.value = false
  }
}

function reload() {
  window.location.reload()
}
</script>

<template>
  <div class="page">
    <div v-if="loadError" role="alert" class="alert alert-error">
      {{ errorText(loadError) }} <RouterLink :to="{ name: 'campaigns' }">{{ t('composer.back') }}</RouterLink>
    </div>
    <p v-else-if="!campaign" class="muted" role="status">{{ t('campaign.loading') }}</p>

    <template v-else>
      <div class="head">
        <RouterLink :to="{ name: 'campaign-content', params: { id } }" class="back" :aria-label="t('review.back')">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M15 6l-6 6 6 6" /></svg>
        </RouterLink>
        <h1 class="title">{{ briefTitle(campaign.request_text) }}</h1>
        <span class="status">{{ campaign.status === 'draft' ? t('review.badge') : t(`status.${campaign.status}`) }}</span>
      </div>

      <div v-if="campaign.status !== 'draft'" class="alert alert-info">{{ t('campaign.notEditable', { status: t(`status.${campaign.status}`) }) }} <RouterLink :to="{ name: 'campaign', params: { id: campaign.id } }">{{ t('campaign.openDelivery') }}</RouterLink></div>

      <template v-else>
        <StepBar :current="3" :campaign-id="id" />
        <div v-if="stale" role="alert" class="alert alert-warning">{{ t('campaign.staleContent') }}</div>
        <!-- 409: approved or edited elsewhere since this page loaded (review 4). -->
        <div v-if="conflict" role="alert" class="alert alert-warning">
          <span>{{ t('error.changed') }}</span>
          <button type="button" class="btn btn-sm" @click="reload">{{ t('campaign.reload') }}</button>
        </div>
        <div v-else-if="approveError" role="alert" class="alert alert-error">{{ errorText(approveError) }}</div>

        <div class="columns">
          <div class="main">
            <section class="card section" aria-labelledby="aud-title">
              <div class="section-head">
                <h2 id="aud-title" class="section-title">{{ t('review.audience') }}</h2>
                <RouterLink v-if="!isResend" :to="{ name: 'campaign', params: { id } }">{{ t('review.editRules') }}</RouterLink>
              </div>
              <p v-if="isResend" class="muted">{{ t('audience.resendNote') }}</p>
              <ul v-else class="rules">
                <li v-for="r in rules" :key="r">{{ r }}</li>
                <li>{{ t('rules.consent') }}</li>
              </ul>
              <div class="numbers">
                <div class="box estimate-box">
                  <span class="tag">{{ t('review.estimateNow') }}</span>
                  <span class="big">{{ formatNumber(counts.total) }} <small>{{ t('review.customers') }}</small></span>
                  <span class="muted">
                    <template v-for="(c, i) in channels" :key="c">{{ i ? ' · ' : '' }}{{ t(c === 'sms' ? 'channel.sms' : 'channel.email') }} {{ formatNumber(counts[c] ?? 0) }}</template>
                    · {{ t('review.messageCount', { count: formatNumber(messages) }) }}
                  </span>
                </div>
                <div class="box actual-box">
                  <span class="tag">{{ t('review.actual') }}</span>
                  <span class="big">—</span>
                  <span class="muted">{{ t('review.actualHint') }}</span>
                </div>
              </div>
            </section>

            <section class="card section" aria-labelledby="msg-title">
              <div class="section-head">
                <h2 id="msg-title" class="section-title">{{ t('review.messages') }}</h2>
                <span class="muted">
                  {{ t('review.shownAs', { name: `${sample.first_name} ${sample.last_name}` }) }} ·
                  <RouterLink :to="{ name: 'campaign-content', params: { id } }">{{ t('review.editContent') }}</RouterLink>
                </span>
              </div>
              <MessagePreview v-if="campaign.content" :content="campaign.content" :channels="channels" />
              <p v-else class="muted">{{ t('content.empty') }}</p>
              <p v-if="over?.sms" class="estimate"><span class="tag">{{ t('content.estimate') }}</span> {{ t('review.overSms', { count: formatNumber(over.sms) }) }}</p>
              <p v-if="over?.email" class="estimate"><span class="tag">{{ t('content.estimate') }}</span> {{ t('review.overEmail', { count: formatNumber(over.email) }) }}</p>
            </section>
          </div>

          <aside class="side">
            <section class="card section" aria-labelledby="when-title">
              <h2 id="when-title" class="section-title">{{ t('review.when') }}</h2>
              <p v-if="note" class="suggestion" :class="{ unusable: !suggested }">
                {{ t(suggested ? 'review.suggested' : 'review.suggestedUnusable') }} <q>{{ note }}</q>
              </p>
              <div class="options" role="radiogroup" :aria-labelledby="'when-title'">
                <label class="option" :class="{ on: when === 'now' }">
                  <input v-model="when" type="radio" name="when" value="now" />
                  <span><strong>{{ t('review.now') }}</strong><small>{{ t('review.nowHint') }}</small></span>
                </label>
                <label class="option" :class="{ on: when === 'later' }">
                  <input v-model="when" type="radio" name="when" value="later" />
                  <span><strong>{{ t('review.later') }}</strong><small>{{ t('review.laterHint') }}</small></span>
                </label>
              </div>
              <label v-if="when === 'later'" class="field">
                {{ t('review.dateTime', { zone: browserZone() }) }}
                <input v-model="localAt" class="input" type="datetime-local" />
              </label>
              <label class="check">
                <input v-model="reviewed" type="checkbox" />
                {{ t('review.reviewed') }}
              </label>
              <!-- Focused (not role=alert) so it is read once, with its label. -->
              <span v-if="tried && blockers.length" id="missing-label" class="sr-only">{{ t('review.missing') }}</span>
              <ul v-if="tried && blockers.length" ref="blockerList" class="blockers" tabindex="-1" aria-labelledby="missing-label">
                <li v-for="b in blockers" :key="b">{{ t(`blocker.${b}`) }}</li>
              </ul>
              <button type="button" class="btn btn-primary btn-lg" :class="{ busy: busy || blockers.length > 0 }" :aria-disabled="busy || blockers.length > 0" @click="openConfirm">
                {{ t(when === 'later' ? 'review.approveLater' : 'review.approveNow') }}
              </button>
              <p class="muted">{{ t('review.confirmOnce') }}</p>
            </section>
          </aside>
        </div>

        <div class="footer">
          <RouterLink :to="{ name: 'campaign-content', params: { id } }" class="btn">{{ t('review.back') }}</RouterLink>
        </div>
      </template>
    </template>

    <ConfirmDialog
      :open="confirmOpen"
      :title="when === 'later' && at ? t('review.confirmLater', { when: formatDateTime(at) }) : t('review.confirmNow')"
      :confirm-label="t(when === 'later' ? 'review.schedule' : 'review.sendNow')"
      :cancel-label="t('review.goBack')"
      @confirm="approve"
      @close="confirmOpen = false"
    >
      <div class="est">
        <div v-if="channels.includes('sms')"><strong>~{{ formatNumber(counts.sms) }}</strong><span>{{ t('review.estSms') }}</span></div>
        <div v-if="channels.includes('email')"><strong>~{{ formatNumber(counts.email) }}</strong><span>{{ t('review.estEmail') }}</span></div>
        <div v-if="over && (over.sms || over.email)"><strong>~{{ formatNumber((over.sms ?? 0) + (over.email ?? 0)) }}</strong><span>{{ t('review.estSkipped') }}</span></div>
      </div>
      <p>{{ t('review.confirmBody') }}</p>
    </ConfirmDialog>
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
  align-items: center;
  gap: 12px;
}
.back {
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  border-radius: var(--radius);
  border: 1px solid var(--line-strong);
  background: var(--surface);
  color: var(--ink);
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.title {
  flex: 1 1 260px;
  min-width: 0;
  font-size: 22px;
}
.status {
  font-size: 12px;
  font-weight: 600;
  padding: 4px 9px;
  border-radius: 999px;
  background: #fdf3e2;
  color: #7a4505;
}
.muted {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
}
.columns {
  display: flex;
  flex-wrap: wrap;
  gap: 20px;
  align-items: flex-start;
}
.main {
  flex: 3 1 520px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.side {
  flex: 2 1 320px;
  min-width: 0;
  position: sticky;
  top: 16px;
}
.section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.section-head {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: baseline;
  gap: 10px;
}
.section-title {
  margin: 0;
  font-size: 16px;
  font-weight: 650;
}
.rules {
  margin: 0;
  padding-left: 18px;
  font-size: 14px;
  color: var(--ink-2);
}
.numbers {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 10px;
}
.box {
  border-radius: 12px;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.estimate-box {
  border: 1px solid #d4d1f6;
  background: #f6f5fe;
}
.actual-box {
  border: 1.5px dashed #9aa3b5;
}
.tag {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--primary-ink);
}
.big {
  font-size: 26px;
  font-weight: 700;
}
.big small {
  font-size: 13px;
  font-weight: 500;
  color: var(--ink-2);
}
.estimate {
  margin: 0;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: #fdf3e2;
  border: 1px solid var(--notice-line);
  font-size: 13px;
  color: #5a3a0a;
}
.suggestion {
  margin: 0;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--primary-soft);
  font-size: 13px;
  color: var(--primary-ink);
}
.suggestion.unusable {
  background: #fdf3e2;
  color: #5a3a0a;
}
.options {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}
.option {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  padding: 12px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius);
  cursor: pointer;
}
.option.on {
  border: 2px solid var(--primary);
  background: #f6f5fe;
}
.option span {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.option small {
  font-size: 12px;
  color: var(--ink-2);
}
.option input,
.check input {
  width: 16px;
  height: 16px;
  margin-top: 2px;
  accent-color: var(--primary);
}
.field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-2);
}
.check {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  font-size: 13px;
  color: var(--ink-2);
  cursor: pointer;
}
.blockers {
  margin: 0;
  padding: 10px 12px 10px 28px;
  border-radius: var(--radius-sm);
  background: var(--danger-soft);
  border: 1px solid var(--danger-soft-line);
  font-size: 13px;
  color: var(--danger-ink);
}
.btn.busy {
  background: var(--primary-busy);
  box-shadow: none;
}
.est {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
  gap: 8px;
  margin-bottom: 10px;
  text-align: center;
}
.est div {
  background: var(--surface-muted);
  border-radius: var(--radius);
  padding: 10px;
  display: flex;
  flex-direction: column;
}
.est strong {
  font-size: 17px;
  color: var(--ink);
}
.est span {
  font-size: 12px;
  color: var(--muted);
}
.footer {
  padding-top: 18px;
  border-top: 1px solid var(--line);
}
</style>
