<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../api'
import { channelsKeepingText } from '../campaign/channels'
import { campaigns, loadCatalog } from '../campaign/client'
import { defaultForm, fromForm, ruleErrors, ruleKey, toForm, withoutRule, withRule, type RuleForm } from '../campaign/rules'
import { createSaver } from '../campaign/saver'
import { briefTitle } from '../campaign/title'
import { writesFirst } from '../content/languages'
import { outdatedLanguages } from '../content/stale'
import type { Campaign, Catalog, Channel, FieldError, Lang, Rule } from '../campaign/types'
import AudiencePanel from '../components/AudiencePanel.vue'
import ChannelPicker from '../components/ChannelPicker.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DeliveryStatus from '../components/DeliveryStatus.vue'
import RuleChips from '../components/RuleChips.vue'
import RuleEditor from '../components/RuleEditor.vue'
import StatusBadge from '../components/StatusBadge.vue'
import StepBar from '../components/StepBar.vue'
import { formatAnd, t, type Key } from '../i18n'
import { aiReason, errorText } from '../i18n/errors'

const route = useRoute()
const router = useRouter()

const campaign = ref<Campaign | null>(null)
const catalog = ref<Catalog | null>(null)
const parent = ref<Campaign | null>(null)
// Messages are kept as errors, keys or text functions, so they follow a
// language switch while on screen (D44).
type Text = () => string
const loadError = ref<unknown>(null)
const saveError = ref<unknown>(null)
const rulesProblem = ref<Text[]>([])
const notice = ref<Key | null>(null) // announced politely: what just happened
const change = ref<number | null>(null)
// D40, D66: the server marks each language written for an earlier brief.
const staleContent = computed(() => outdatedLanguages(campaign.value?.languages).length > 0)

const saver = createSaver<Campaign>(
  () => campaign.value?.version ?? 0,
  (c) => (campaign.value = c),
)
const saveKeys: Record<string, Key | null> = { idle: null, saving: 'save.saving', saved: 'save.saved', error: 'save.error', conflict: 'save.conflict' }
const saveLabel = computed(() => {
  const key = saveKeys[saver.state.value]
  return key ? t(key) : ''
})

// The layout gives each campaign its own page instance, so the id, the save
// queue and every message belong to exactly one campaign.
const id = Number(route.params.id)
const isDraft = computed(() => campaign.value?.status === 'draft')
const isResend = computed(() => campaign.value?.parent_campaign_id != null)
const locked = computed(() => saver.state.value === 'conflict')

// One-step creation (D71): a new draft's languages are ticked here, the
// suggestion until the marketer changes it; only languages its audience speaks.
const firstWrite = computed(() => !!campaign.value && writesFirst(campaign.value))
const picked = ref<Lang[] | null>(null)
const chosen = computed(() =>
  (picked.value ?? campaign.value?.recommendation?.languages ?? []).filter((l) => (campaign.value?.counts?.by_language?.[l]?.total ?? 0) > 0),
)
function pick(l: Lang, on: boolean) {
  const codes = catalog.value?.languages.map((c) => c.code) ?? []
  picked.value = codes.filter((c) => (c === l ? on : chosen.value.includes(c)))
}
// write=ai asks the content step to start the AI once; it removes it from the URL.
const contentLink = (ai: boolean) => ({
  name: 'campaign-content',
  params: { id },
  query: { languages: chosen.value.join(','), ...(ai ? { write: 'ai' } : {}) },
})

async function load() {
  loadError.value = null
  campaign.value = null
  try {
    const [c, cat] = await Promise.all([campaigns.get(id), loadCatalog()])
    campaign.value = c
    catalog.value = cat
    brief.value = c.request_text
    // A resend names its original campaign (D31); without it the page still works.
    if (c.parent_campaign_id) parent.value = await campaigns.get(c.parent_campaign_id).catch(() => null)
  } catch (err) {
    loadError.value = err
  }
}
load()

// save runs one change through the queue. A failure shows in the page's
// banner unless the caller shows it where it belongs (banner: false).
async function save(op: (version: number) => Promise<Campaign>, done?: Key, banner = true): Promise<void> {
  saveError.value = null
  try {
    await saver.save(op)
    if (done) notice.value = done
  } catch (err) {
    if (banner && saver.state.value !== 'conflict') saveError.value = err
    throw err
  }
}

// ---- channels ----
// A removed channel's text is kept, not sent, and comes back with it (D67).
const keptChannels = ref<Channel[]>([])
function pickChannels(next: Channel[]) {
  const c = campaign.value!
  const kept = channelsKeepingText(c.channels, next, c.content)
  const before = c.counts?.total
  save((v) => campaigns.setChannels(id, v, next), 'notice.channelsSaved')
    .then(() => {
      change.value = before === undefined ? null : (campaign.value?.counts?.total ?? 0) - before
      keptChannels.value = kept
    })
    .catch(() => {})
}
const channelName = (c: Channel) => t(c === 'sms' ? 'channel.sms' : 'channel.email')
// Inside a sentence: "email" / "e-posta", not "Email".
const channelInline = (c: Channel) => t(c === 'sms' ? 'channel.inline.sms' : 'channel.inline.email')
const channelList = (cs: Channel[]) => formatAnd(cs.map(channelInline))

// ---- rules ----
// key: the rule being edited (field and operator), null for a new rule.
const editor = ref<{ key: string | null; form: RuleForm; errors: Text[]; saving: boolean } | null>(null)
const errorsByKey = ref<Map<string, string[]>>(new Map()) // the backend's field errors, per rule
const chipErrors = computed(() => {
  const byIndex = new Map<number, string[]>()
  campaign.value?.rules?.forEach((r, i) => errorsByKey.value.has(ruleKey(r)) && byIndex.set(i, errorsByKey.value.get(ruleKey(r))!))
  return byIndex
})

function editRule(index: number) {
  const r = campaign.value!.rules![index]!
  if (r.field === 'all_customers') return // nothing to edit: its value is always true
  editor.value = { key: ruleKey(r), form: toForm(r, catalog.value!), errors: [], saving: false }
}
// All customers has no form: it replaces the filters, after a confirmation (D14).
const allConfirm = ref(false)
function addRule(field: string, op: string) {
  if (field === 'all_customers') {
    allConfirm.value = true
    return
  }
  editor.value = { key: null, form: defaultForm(field, op, catalog.value!), errors: [], saving: false }
}
async function useAllCustomers() {
  allConfirm.value = false
  const problems = await saveRules((rules) => withRule(rules, null, { field: 'all_customers', op: 'is', value: true }), null)
  if (problems.length) rulesProblem.value = problems
}

// saveRules saves build(latest rules). The list is built when the save runs,
// not when the marketer clicked, so quick edits never undo each other. It
// returns the problems to show for the rule with targetKey.
async function saveRules(build: (rules: Rule[]) => Rule[], targetKey: string | null): Promise<Text[]> {
  const before = campaign.value?.counts?.total
  let sent: Rule[] = []
  rulesProblem.value = []
  try {
    await save(
      (v) => {
        sent = build(campaign.value?.rules ?? [])
        return campaigns.setRules(id, v, sent)
      },
      'notice.rulesSaved',
      false,
    )
    errorsByKey.value = new Map()
    change.value = before === undefined ? null : (campaign.value?.counts?.total ?? 0) - before
    return []
  } catch (err) {
    if (saver.state.value === 'conflict') return [] // the page banner explains it
    const fieldErrors = err instanceof ApiError ? ((err.body as { field_errors?: FieldError[] })?.field_errors ?? []) : []
    if (fieldErrors.length === 0) return [() => errorText(err)]
    const { byRule, general } = ruleErrors(fieldErrors)
    const byKey = new Map<string, string[]>()
    byRule.forEach((msgs, i) => sent[i] && byKey.set(ruleKey(sent[i]!), msgs))
    errorsByKey.value = byKey
    const own = targetKey === null ? [] : (byKey.get(targetKey) ?? [])
    const others = [...byKey].filter(([k]) => k !== targetKey).flatMap(([, msgs]) => msgs)
    // The server's validation text is English (D44, approach A): shown as details.
    const details = (m: string) => () => `${t('error.invalid')} ${t('error.details', { message: m })}`
    // Never report success: if the problem is on another rule, say so.
    return own.length || general.length
      ? [...own, ...general].map(details)
      : others.map((m) => () => t('rules.anotherRule', { message: details(m)() }))
  }
}

async function applyRule(form: RuleForm) {
  const e = editor.value!
  const next = fromForm(form, catalog.value!)
  e.saving = true
  const problems = await saveRules((rules) => withRule(rules, e.key, next), ruleKey(next))
  e.saving = false
  if (editor.value !== e) {
    if (problems.length) rulesProblem.value = problems // the editor was closed meanwhile
    return
  }
  e.errors = problems
  if (problems.length === 0) editor.value = null
}

async function removeRule(index: number) {
  const key = ruleKey(campaign.value!.rules![index]!)
  const problems = await saveRules((rules) => withoutRule(rules, key), null)
  if (problems.length) rulesProblem.value = problems
}

// ---- brief ----
const brief = ref('')
const briefBusy = ref(false)
const briefProblem = ref<{ err: unknown; reason: string | null } | null>(null)
const reinterpretConfirm = ref(false)
const briefChanged = computed(() => brief.value.trim() !== (campaign.value?.request_text ?? ''))

async function reinterpret() {
  reinterpretConfirm.value = false
  briefProblem.value = null
  saveError.value = null
  briefBusy.value = true
  const before = campaign.value?.counts?.total
  try {
    await saver.save((v) => campaigns.setRequest(id, v, brief.value.trim()))
    notice.value = 'notice.reinterpreted'
    change.value = before === undefined ? null : (campaign.value?.counts?.total ?? 0) - before
  } catch (err) {
    // Nothing was saved (D15); the new text stays so it can be rephrased.
    briefProblem.value = saver.state.value === 'conflict' ? null : { err, reason: aiReason(err) }
  } finally {
    briefBusy.value = false
  }
}

// ---- delete ----
const deleteConfirm = ref(false)
async function deleteDraft() {
  deleteConfirm.value = false
  saveError.value = null
  try {
    await saver.save(async (v) => {
      await campaigns.remove(id, v)
      return campaign.value!
    })
    await router.replace({ name: 'campaigns' })
  } catch (err) {
    if (saver.state.value !== 'conflict') saveError.value = err
  }
}

function reload() {
  window.location.reload() // a fresh start also clears the save queue's conflict state
}
</script>

<template>
  <div class="page">
    <div v-if="loadError" role="alert" class="alert alert-error">
      {{ errorText(loadError) }} <RouterLink :to="{ name: 'campaigns' }">{{ t('composer.back') }}</RouterLink>
    </div>
    <p v-else-if="!campaign || !catalog" class="loading" role="status">{{ t('campaign.loading') }}</p>

    <template v-else>
      <div class="head">
        <RouterLink :to="{ name: 'campaigns' }" class="back" :aria-label="t('composer.back')">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M15 6l-6 6 6 6" /></svg>
        </RouterLink>
        <!-- Campaigns are identified by their brief (D43); the full brief is below. -->
        <h1 class="title">{{ briefTitle(campaign.request_text) }}</h1>
        <StatusBadge :status="campaign.status" />
        <span v-if="isResend" class="status">{{ t('campaign.resendBadge', { channel: channelName(campaign.channels[0]!) }) }}</span>
        <span class="save-state" :class="saver.state.value">{{ saveLabel }}</span>
        <button v-if="isDraft" type="button" class="btn btn-sm btn-danger-outline" :disabled="locked" @click="deleteConfirm = true">{{ t('campaign.delete') }}</button>
      </div>

      <p class="sr-only" role="status">{{ notice ? t(notice) : '' }}</p>
      <div v-if="locked" role="alert" class="alert alert-warning">
        <span>{{ t('error.changed') }}</span>
        <button type="button" class="btn btn-sm" @click="reload">{{ t('campaign.reload') }}</button>
      </div>
      <div v-else-if="saveError" role="alert" class="alert alert-error">{{ errorText(saveError) }}</div>

      <DeliveryStatus
        v-if="!isDraft"
        :campaign="campaign"
        :parent="parent"
        @updated="(c) => (campaign = c)"
        @status="(s) => (campaign!.status = s)"
      />

      <template v-else>
        <StepBar :current="1" :campaign-id="id" />
        <div v-if="staleContent" role="alert" class="alert alert-warning">
          {{ t('campaign.staleContent') }}
        </div>
        <div v-if="keptChannels.length" role="status" class="alert alert-info">
          {{ t('notice.channelTextKept', { channels: channelList(keptChannels) }) }}
        </div>

        <div class="columns">
          <div class="main">
            <section v-if="!isResend" class="card section" aria-labelledby="brief-label">
              <label id="brief-label" for="brief" class="section-title">{{ t('composer.brief') }}</label>
              <textarea id="brief" v-model="brief" rows="3" class="textarea" :readonly="briefBusy || locked" aria-describedby="brief-help" />
              <span id="brief-help" class="help">
                {{ t(briefChanged ? 'campaign.briefEdited' : 'campaign.briefHelp') }}
              </span>
              <div v-if="briefProblem" role="alert" class="alert" :class="briefProblem.reason ? 'alert-warning' : 'alert-error'">
                <span v-if="briefProblem.reason">
                  <strong>{{ t('composer.unsupported') }}</strong>
                  {{ t('composer.aiReason') }} <q>{{ briefProblem.reason }}</q> {{ t('campaign.draftUnchanged') }}
                </span>
                <span v-else>{{ errorText(briefProblem.err) }} {{ t('campaign.draftUnchanged') }}</span>
              </div>
              <div class="bar">
                <ChannelPicker :model-value="campaign.channels" :disabled="locked" @update:model-value="pickChannels" />
                <button type="button" class="btn btn-sm" :class="{ busy: briefBusy }" :aria-disabled="briefBusy || locked" @click="!briefBusy && !locked && (reinterpretConfirm = true)">
                  <span v-if="briefBusy" class="spinner dark" aria-hidden="true" />
                  {{ t(briefBusy ? 'composer.interpreting' : 'campaign.reinterpret') }}
                </button>
              </div>
            </section>

            <section class="card section" aria-labelledby="rules-title">
              <div class="section-head">
                <h2 id="rules-title" class="section-title">{{ t('campaign.rules') }}</h2>
                <span v-if="!isResend" class="help">{{ t('campaign.rulesHelp') }}</span>
              </div>
              <p v-if="isResend" class="help">{{ t('campaign.resendAudience', { channel: channelInline(campaign.channels[0]!) }) }}</p>
              <template v-else>
                <RuleChips
                  :rules="campaign.rules ?? []"
                  :catalog="catalog"
                  :errors="chipErrors"
                  :disabled="locked || briefBusy"
                  @edit="editRule"
                  @add="addRule"
                  @remove="removeRule"
                />
                <div v-if="rulesProblem.length" role="alert" class="alert alert-error">{{ rulesProblem.map((p) => p()).join(' ') }}</div>
              </template>
            </section>
          </div>

          <aside class="side">
            <AudiencePanel
              :counts="campaign.counts"
              :channels="campaign.channels"
              :change="change"
              :resend="isResend"
              :languages="catalog.languages"
              :recommended="campaign.recommendation?.languages"
              :selected="firstWrite ? chosen : undefined"
              @toggle="pick"
            />
          </aside>
        </div>

        <div class="footer">
          <span class="help">{{ t('campaign.next', { channels: channelList(campaign.channels), count: campaign.channels.length }) }}</span>
          <span class="next">
            <template v-if="firstWrite">
              <button type="button" class="btn btn-primary btn-lg" :aria-disabled="!chosen.length" @click="chosen.length && router.push(contentLink(true))">
                {{ t('campaign.writeWithAI') }}
              </button>
              <span v-if="!chosen.length" class="help" role="status">{{ t('campaign.pickLanguage') }}</span>
              <RouterLink :to="contentLink(false)" class="btn btn-lg">{{ t('campaign.writeMyself') }}</RouterLink>
            </template>
            <RouterLink v-else :to="{ name: 'campaign-content', params: { id } }" class="btn btn-primary btn-lg">{{ t('campaign.generate') }}</RouterLink>
          </span>
        </div>
      </template>
    </template>

    <RuleEditor
      v-if="catalog"
      :open="!!editor"
      :form="editor?.form ?? null"
      :catalog="catalog"
      :server-errors="editor?.errors.map((e) => e())"
      :saving="editor?.saving"
      @apply="applyRule"
      @close="editor = null"
    />


    <ConfirmDialog
      :open="reinterpretConfirm"
      :title="t('confirm.reinterpret')"
      :confirm-label="t('confirm.reinterpretAction')"
      @confirm="reinterpret"
      @close="reinterpretConfirm = false"
    >
      <p>{{ t('confirm.reinterpretBody') }}</p>
      <p v-if="campaign?.content">{{ t('confirm.reinterpretContent') }}</p>
    </ConfirmDialog>

    <ConfirmDialog
      :open="allConfirm"
      :title="t('confirm.allCustomersTitle')"
      :confirm-label="t('confirm.allCustomersConfirm')"
      @confirm="useAllCustomers"
      @close="allConfirm = false"
    >
      <p>{{ t('confirm.allCustomersBody') }}</p>
    </ConfirmDialog>

    <ConfirmDialog
      :open="deleteConfirm"
      :title="t('confirm.delete')"
      :confirm-label="t('confirm.deleteAction')"
      :cancel-label="t('confirm.deleteKeep')"
      danger
      @confirm="deleteDraft"
      @close="deleteConfirm = false"
    >
      <p><strong>{{ campaign ? briefTitle(campaign.request_text) : '' }}</strong></p>
      <p>{{ t('confirm.deleteBody') }}</p>
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
  background: #f1f0ec;
  color: #3e465c;
  text-transform: capitalize;
}
.save-state {
  font-size: 12px;
  color: var(--muted);
}
.save-state.error,
.save-state.conflict {
  color: var(--danger);
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
.help {
  font-size: 12px;
  color: var(--muted);
  margin: 0;
}
.textarea {
  width: 100%;
  resize: vertical;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius);
  padding: 14px;
  background: var(--surface-muted);
  font: 400 15px/1.55 var(--font);
  color: var(--ink);
}
.bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.spinner.dark {
  border-color: rgba(18, 26, 51, 0.2);
  border-top-color: var(--ink);
}
.alert {
  justify-content: space-between;
  align-items: center;
}
.footer {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding-top: 18px;
  border-top: 1px solid var(--line);
}
.next {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
}
.next .btn[aria-disabled='true'] {
  background: var(--primary-busy);
  box-shadow: none;
  cursor: not-allowed;
}
.loading {
  color: var(--muted);
}
</style>
