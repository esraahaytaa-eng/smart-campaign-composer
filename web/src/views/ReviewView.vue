<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { campaigns, loadCatalog, type Estimate } from '../campaign/client'
import { ruleLabel, targetsEveryone } from '../campaign/rules'
import { briefTitle } from '../campaign/title'
import type { Campaign, Catalog, Channel, Lang } from '../campaign/types'
import { languageName, orderLanguages } from '../content/languages'
import { approvalBlockers, unapprovedLanguages, type Blocker } from '../content/approval'
import { samplesFor } from '../content/preview'
import { outdatedLanguages } from '../content/stale'
import { browserZone, fromLocalInput, scheduleProblem, suggestion, toLocalInput } from '../content/schedule'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import MessagePreview from '../components/MessagePreview.vue'
import StepBar from '../components/StepBar.vue'
import { formatAnd, formatDateTime, formatNumber, t } from '../i18n'
import { errorText } from '../i18n/errors'
import { ApiError } from '../api'

const route = useRoute()
const router = useRouter()
const id = Number(route.params.id)
// The brief was re-interpreted after the messages were written (D40).
const stale = computed(() => outdatedLanguages(campaign.value?.languages).length > 0) // D40, D66

const campaign = ref<Campaign | null>(null)
const catalog = ref<Catalog['languages']>([])
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
    const [c, cat] = await Promise.all([campaigns.get(id), loadCatalog()])
    catalog.value = cat.languages
    campaign.value = c
    active.value = c.default_language ?? ''
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
const over = computed(() => estimate.value?.over_limit?.by_language?.[active.value] ?? null)
const overB = computed(() => estimate.value?.over_limit_b?.by_language?.[active.value] ?? null)
const LETTERS = ['A', 'B'] as const // i18n-ignore: version names, the same in every language
const messages = computed(() => channels.value.reduce((n, c) => n + (counts.value[c] ?? 0), 0))
const blockers = computed<Blocker[]>(() =>
  campaign.value
    ? approvalBlockers({
        channels: channels.value,
        content: campaign.value.content,
        defaultLanguage: campaign.value.default_language,
        languages: campaign.value.languages ?? {},
        abTest: campaign.value.ab_test,
        contentB: campaign.value.content_b,
        audience: counts.value.total,
        when: when.value,
        at: at.value,
        reviewed: reviewed.value,
        scheduleProblem: at.value ? scheduleProblem(at.value, now.value) : null,
      })
    : [],
)
const rules = computed(() => (campaign.value?.rules ?? []).map(ruleLabel))
const everyone = computed(() => targetsEveryone(campaign.value?.rules)) // D14: shown loudly before approval
const isResend = computed(() => campaign.value?.parent_campaign_id != null)
// Each language on its own (D64, D66): who gets its text, its review state
// and a preview with customers who speak it.
const active = ref<Lang>('')
const name = (l: Lang) => languageName(l, catalog.value)
const langs = computed(() => orderLanguages(Object.keys(campaign.value?.content ?? {}), campaign.value?.default_language ?? null, catalog.value))
const delivery = computed(() => estimate.value?.delivery ?? campaign.value?.delivery ?? {})
const sample = computed(() => samplesFor(active.value)[0]!.vars)
const activeContent = computed(() => campaign.value?.content?.[active.value] ?? null)
const activeContentB = computed(() => campaign.value?.content_b?.[active.value] ?? null)
function stateOf(l: Lang): 'approved' | 'outdated' | 'needsReview' {
  const s = campaign.value?.languages?.[l]
  if (s?.approved_at) return 'approved'
  return s?.outdated ? 'outdated' : 'needsReview'
}
const blockerList = ref<HTMLElement>()
// The unapproved-language blocker names the languages, so a single one isn't read as "every language".
const blockerText = (b: Blocker) =>
  b === 'unapprovedLanguage' && campaign.value
    ? t('blocker.unapprovedLanguage', { names: formatAnd(unapprovedLanguages({ content: campaign.value.content, languages: campaign.value.languages ?? {} }).map(name)) })
    : t(`blocker.${b}`)

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
              <p v-else-if="everyone" class="alert alert-warning">{{ t('review.allCustomers', { count: formatNumber(counts.total) }) }}</p>
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
              <ul v-if="langs.length" class="by-language" :aria-label="t('review.byLanguage')">
                <li v-for="l in langs" :key="l">
                  <span class="lang-name">
                    <strong>{{ name(l) }}</strong>
                    <span v-if="l === campaign.default_language" class="tag">{{ t('content.lang.default') }}</span>
                    <span :class="['state', stateOf(l)]">{{ t(`content.lang.${stateOf(l)}`) }}</span>
                  </span>
                  <span class="muted">
                    <template v-for="(c, i) in channels" :key="c">
                      {{ i ? ' · ' : '' }}{{ t(c === 'sms' ? 'channel.sms' : 'channel.email') }} {{ formatNumber(delivery[c]?.[l]?.own ?? 0) }}
                      <template v-if="delivery[c]?.[l]?.fallback">{{ t('review.fallback', { count: formatNumber(delivery[c]![l]!.fallback) }) }}</template>
                    </template>
                  </span>
                </li>
              </ul>
              <p v-if="langs.length > 1 || campaign.default_language === 'und'" class="muted">{{ t('review.fallbackHint') }}</p>
              <div v-if="langs.length > 1" class="tabs" role="group" :aria-label="t('content.languages')">
                <button v-for="l in langs" :key="l" type="button" class="tab" :class="{ current: l === active }" :aria-pressed="l === active" @click="active = l">
                  {{ name(l) }}
                </button>
              </div>
              <p v-if="campaign.ab_test" class="muted">{{ t('ab.split') }}</p>
              <div :class="{ 'ab-compare': campaign.ab_test }">
                <div>
                  <h3 v-if="campaign.ab_test" class="version-title">{{ t('ab.version', { v: LETTERS[0] }) }}</h3>
                  <MessagePreview v-if="activeContent" :key="active" :content="activeContent" :channels="channels" :samples="samplesFor(active)" />
                  <p v-else class="muted">{{ t('content.empty') }}</p>
                  <p v-if="over?.sms" class="estimate"><span class="tag">{{ t('content.estimate') }}</span> {{ t('review.overSms', { count: formatNumber(over.sms) }) }}</p>
                  <p v-if="over?.email" class="estimate"><span class="tag">{{ t('content.estimate') }}</span> {{ t('review.overEmail', { count: formatNumber(over.email) }) }}</p>
                </div>
                <div v-if="campaign.ab_test">
                  <h3 class="version-title">{{ t('ab.version', { v: LETTERS[1] }) }}</h3>
                  <MessagePreview v-if="activeContentB" :key="`${active}-b`" :content="activeContentB" :channels="channels" :samples="samplesFor(active)" />
                  <p v-else class="muted">{{ t('ab.missingB') }}</p>
                  <p v-if="overB?.sms" class="estimate"><span class="tag">{{ t('content.estimate') }}</span> {{ t('review.overSms', { count: formatNumber(overB.sms) }) }}</p>
                  <p v-if="overB?.email" class="estimate"><span class="tag">{{ t('content.estimate') }}</span> {{ t('review.overEmail', { count: formatNumber(overB.email) }) }}</p>
                </div>
              </div>
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
                <li v-for="b in blockers" :key="b">{{ blockerText(b) }}</li>
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
      <p v-if="everyone"><strong>{{ t('review.allCustomers', { count: formatNumber(counts.total) }) }}</strong></p>
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
.ab-compare {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 16px;
}
.version-title {
  font-size: 14px;
  margin: 0 0 8px;
}
.by-language {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 13px;
}
.by-language li {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 4px 12px;
}
.lang-name {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.state {
  font-size: 12px;
  color: var(--muted);
}
.state.approved {
  color: var(--ink-2);
}
.state.outdated,
.state.needsReview {
  color: #5a3a0a;
}
.tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.tab {
  min-height: 40px;
  padding: 6px 12px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius);
  background: var(--surface);
  color: var(--ink);
  font-size: 14px;
  cursor: pointer;
}
.tab.current {
  border-color: var(--primary);
  box-shadow: inset 0 -3px 0 var(--primary);
  font-weight: 650;
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
