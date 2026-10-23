<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute } from 'vue-router'
import { ApiError } from '../api'
import { campaigns, streamContent, type Estimate } from '../campaign/client'
import { createSaver } from '../campaign/saver'
import { briefTitle } from '../campaign/title'
import type { Campaign, Channel, Content } from '../campaign/types'
import { render, SAMPLES } from '../content/preview'
import { chosenContent, fromContent, generationOutcome, hasUnsavedChanges, needsOverwriteConfirm, type Draft } from '../content/editing'
import { contentWasStale, markContentFresh } from '../content/stale'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import MessageField from '../components/MessageField.vue'
import MessagePreview from '../components/MessagePreview.vue'
import StepBar from '../components/StepBar.vue'
import { formatNumber, t, type Key } from '../i18n'
import { session } from '../session'
import { errorText } from '../i18n/errors'

const LIMITS = { sms: 160, subject: 80, body: 2000 } // after personalization (D2)

const route = useRoute()
const id = Number(route.params.id) // each campaign gets its own page instance

const campaign = ref<Campaign | null>(null)
const parent = ref<Campaign | null>(null)
const estimate = ref<Estimate | null>(null)
const loadError = ref<unknown>(null)
const saveError = ref<unknown>(null)
const notice = ref<Key | null>(null)
const draft = ref<Draft>(fromContent(null))
const fieldErrors = ref<Record<string, string[]>>({})
const incomplete = ref(false)
const stale = ref(contentWasStale(id))

const saver = createSaver<Campaign>(
  () => campaign.value?.version ?? 0,
  (c) => (campaign.value = c),
)
const locked = computed(() => saver.state.value === 'conflict')
const isDraft = computed(() => campaign.value?.status === 'draft')
const channels = computed<Channel[]>(() => campaign.value?.channels ?? [])
const uses = (c: Channel) => channels.value.includes(c)

function fromCampaign(c: Campaign) {
  draft.value = fromContent(c.content)
}

async function refreshEstimate() {
  estimate.value = await campaigns.estimate(id).catch(() => estimate.value)
}

async function load() {
  try {
    const c = await campaigns.get(id)
    campaign.value = c
    fromCampaign(c)
    // A resend shows its original message for reference (D48).
    if (c.parent_campaign_id) campaigns.get(c.parent_campaign_id).then((p) => (parent.value = p), () => {})
    await refreshEstimate()
  } catch (err) {
    loadError.value = err
  }
}
load()

// The content to save: only the chosen channels.
const chosen = (): Content => chosenContent(draft.value, channels.value)
const complete = () =>
  (!uses('sms') || draft.value.sms.body.trim() !== '') &&
  (!uses('email') || (draft.value.email.subject.trim() !== '' && draft.value.email.body.trim() !== ''))
const unsaved = () => !!campaign.value && hasUnsavedChanges(draft.value, campaign.value.content, channels.value)

// Autosave when a field loses focus, after a pause in typing, and before
// leaving; the content is read when the save runs. Returns whether nothing
// is left unsaved. quiet: a typing pause doesn't nag about empty fields.
async function commit(quiet = false): Promise<boolean> {
  clearTimeout(pause) // this save covers a pending typing pause: one edit, one save
  if (!campaign.value || streaming.value || locked.value) return !unsaved()
  if (!unsaved()) return true
  if (!complete()) {
    if (!quiet) incomplete.value = true
    return false
  }
  incomplete.value = false
  saveError.value = null
  let sent = false
  try {
    // The save may wait in the queue: check again when it runs, so text
    // cleared or saved meanwhile is never sent.
    await saver.save(async (v) => {
      if (!complete() || !unsaved()) return campaign.value!
      sent = true
      return campaigns.setContent(id, v, chosen())
    })
    if (!sent) {
      if (!complete() && !quiet) incomplete.value = true
      return !unsaved()
    }
    fieldErrors.value = {}
    notice.value = 'content.saved'
    stale.value = false
    markContentFresh(id)
    await refreshEstimate()
    return !unsaved()
  } catch (err) {
    // Session expired: the router is already on its way to sign-in; nothing
    // more can be saved, so don't retry or ask (re-review: a 401 loop).
    if (err instanceof ApiError && err.status === 401) return true
    const list = err instanceof ApiError ? ((err.body as { field_errors?: { field: string; message: string }[] })?.field_errors ?? []) : []
    if (list.length) {
      const byField: Record<string, string[]> = {}
      // The server's validation text is English (D44): shown as details.
      for (const e of list) (byField[e.field] ??= []).push(`${t('error.invalid')} ${t('error.details', { message: e.message })}`)
      fieldErrors.value = byField
    } else if (saver.state.value !== 'conflict') saveError.value = err
    return false
  }
}

// Typing pause: save without waiting for the field to lose focus.
let pause: ReturnType<typeof setTimeout> | undefined
watch(
  draft,
  () => {
    clearTimeout(pause)
    pause = setTimeout(() => commit(true), 1500)
  },
  { deep: true },
)

// Leaving (link, Back, a step, another campaign's content page) saves first;
// if that can't happen, ask. Another campaign's content page is the same
// route with a new id, which is a route *update*, so both guards are needed.
async function beforeLeaving(): Promise<boolean> {
  clearTimeout(pause)
  // Signed out (session expired, or log out): nothing can be saved, so leave
  // without saving or asking; otherwise every redirect to sign-in would retry.
  if (!session.value) return true
  if (streaming.value) {
    if (!window.confirm(t('content.leaveWhileWriting'))) return false
    stop?.abort()
    return true
  }
  if (await commit()) return true
  return window.confirm(t(complete() ? 'content.leaveUnsaved' : 'content.leaveIncomplete'))
}
onBeforeRouteLeave(beforeLeaving)
onBeforeRouteUpdate((to, from) => (to.params.id === from.params.id ? true : beforeLeaving()))

// Closing or reloading the tab: the browser's own "leave site?" prompt.
function guardUnload(e: BeforeUnloadEvent) {
  if (streaming.value || unsaved()) e.preventDefault()
}
onMounted(() => window.addEventListener('beforeunload', guardUnload))
onBeforeUnmount(() => {
  window.removeEventListener('beforeunload', guardUnload)
  clearTimeout(pause)
})

// ---- AI writing (D34): live text while it streams; nothing saved if stopped ----
const streaming = ref(false)
const retrying = ref(false)
const live = ref<Record<string, string>>({})
const aiProblem = ref<string | null>(null) // the stream's own error text (English) as details
let stop: AbortController | null = null
const regenerateConfirm = ref(false)

// Ask before the AI replaces anything: saved content or text not saved yet.
function askToWrite() {
  if (needsOverwriteConfirm(draft.value, campaign.value?.content ?? null, channels.value)) regenerateConfirm.value = true
  else write()
}

async function write() {
  regenerateConfirm.value = false
  aiProblem.value = null
  saveError.value = null
  notice.value = null
  live.value = {}
  retrying.value = false
  streaming.value = true
  stop = new AbortController()
  const signal = stop.signal
  let finished = false
  // Set inside the queued save; an object so TypeScript sees it change there.
  const result: { outcome: ReturnType<typeof generationOutcome> } = { outcome: 'failed' }
  clearTimeout(pause)
  try {
    await saver.save(async (version) => {
      try {
        await streamContent(
          id,
          version,
          (e) => {
            const data = e.data as Record<string, unknown>
            if (e.event === 'delta') live.value[data.field as string] = (live.value[data.field as string] ?? '') + (data.text as string)
            else if (e.event === 'retry') {
              retrying.value = true
              live.value = {}
            } else if (e.event === 'done') finished = true
            else if (e.event === 'error') aiProblem.value = String(data.message ?? '')
          },
          signal,
        )
      } catch (err) {
        if (!(err instanceof DOMException && err.name === 'AbortError')) throw err
      }
      const fresh = await campaigns.get(id)
      result.outcome = generationOutcome({ finished, aborted: signal.aborted, sentVersion: version, version: fresh.version })
      // A newer version without a finished generation is someone else's
      // change: show "changed elsewhere" instead of adopting it.
      if (result.outcome === 'conflict') throw new ApiError('changed elsewhere', 409) // i18n-ignore: the page shows its conflict banner
      return fresh
    })
    if (result.outcome === 'done') {
      fromCampaign(campaign.value!)
      notice.value = 'content.done'
      fieldErrors.value = {}
      stale.value = false
      markContentFresh(id)
      await refreshEstimate()
    } else if (result.outcome === 'stopped') notice.value = 'content.stopped'
  } catch (err) {
    if (saver.state.value !== 'conflict') saveError.value = err
  } finally {
    streaming.value = false
    stop = null
  }
}
onBeforeUnmount(() => stop?.abort())

function reload() {
  window.location.reload() // a fresh start also clears the save queue's conflict state
}

// While the AI writes, the fields show its text so far.
const shown = (field: 'sms.body' | 'email.subject' | 'email.body', value: string) => (streaming.value ? (live.value[field] ?? '') : value)
const longest = SAMPLES.find((s) => s.id === 'long')!.vars
const len = (text: string) => [...render(text, longest)].length
const previewContent = computed<Content>(() => ({
  sms: { body: shown('sms.body', draft.value.sms.body) },
  email: { subject: shown('email.subject', draft.value.email.subject), body: shown('email.body', draft.value.email.body) },
}))
const over = computed(() => estimate.value?.over_limit ?? null)
const statusText = computed(() => {
  if (streaming.value) return t(retrying.value ? 'content.retrying' : 'content.writing')
  if (aiProblem.value) return t('content.aiError')
  return notice.value ? t(notice.value) : ''
})
</script>

<template>
  <div class="page">
    <div v-if="loadError" role="alert" class="alert alert-error">
      {{ errorText(loadError) }} <RouterLink :to="{ name: 'campaigns' }">{{ t('composer.back') }}</RouterLink>
    </div>
    <p v-else-if="!campaign" class="muted" role="status">{{ t('campaign.loading') }}</p>

    <template v-else>
      <div class="head">
        <RouterLink :to="{ name: 'campaign', params: { id } }" class="back" :aria-label="t('content.back')">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M15 6l-6 6 6 6" /></svg>
        </RouterLink>
        <h1 class="title">{{ briefTitle(campaign.request_text) }}</h1>
        <span class="status">{{ t(`status.${campaign.status}`) }}</span>
        <span class="save-state">{{ saver.state.value === 'saving' && !streaming ? t('save.saving') : saver.state.value === 'saved' ? t('save.saved') : '' }}</span>
      </div>

      <div v-if="!isDraft" class="alert alert-info">{{ t('campaign.notEditable', { status: t(`status.${campaign.status}`) }) }} <RouterLink :to="{ name: 'campaign', params: { id: campaign.id } }">{{ t('campaign.openDelivery') }}</RouterLink></div>

      <template v-else>
        <StepBar :current="2" :campaign-id="id" />
        <p class="audience-line">
          {{ t('content.audienceEst') }}: <strong>{{ formatNumber(campaign.counts?.total ?? 0) }}</strong>
          <template v-for="c in channels" :key="c"> · {{ t(c === 'sms' ? 'channel.sms' : 'channel.email') }} {{ formatNumber(campaign.counts?.[c] ?? 0) }}</template>
          · <RouterLink :to="{ name: 'campaign', params: { id } }">{{ t('content.editAudience') }}</RouterLink>
        </p>

        <!-- One status region, always mounted, so screen readers announce changes. -->
        <p class="sr-only" role="status">{{ statusText }}</p>
        <div v-if="locked" role="alert" class="alert alert-warning">
          <span>{{ t('error.changed') }}</span>
          <button type="button" class="btn btn-sm" @click="reload">{{ t('campaign.reload') }}</button>
        </div>
        <div v-else-if="saveError" role="alert" class="alert alert-error">{{ errorText(saveError) }}</div>
        <div v-if="stale" role="alert" class="alert alert-warning">{{ t('campaign.staleContent') }}</div>

        <div class="ai-bar" :class="{ busy: streaming }">
          <span v-if="streaming">
            <strong>{{ t(retrying ? 'content.retrying' : 'content.writing') }}</strong> {{ t('content.writingHint') }}
          </span>
          <span v-else-if="aiProblem" class="problem">
            {{ t('content.aiError') }} {{ t('error.details', { message: aiProblem }) }}
          </span>
          <span v-else-if="notice === 'content.stopped' || notice === 'content.done'">{{ t(notice) }}</span>
          <span v-else-if="!campaign.content">{{ t('content.empty') }}</span>
          <span v-else />
          <button v-if="streaming" type="button" class="btn btn-sm" @click="stop?.abort()">{{ t('content.stop') }}</button>
          <button v-else type="button" class="btn btn-sm btn-soft" :disabled="locked" @click="askToWrite">
            {{ t(campaign.content ? 'content.regenerate' : 'content.generate') }}
          </button>
        </div>

        <div class="columns">
          <div class="main">
            <section v-if="parent" class="card section original">
              <p>{{ t('resend.contentLead', { title: briefTitle(parent.request_text), channel: t(`channel.inline.${campaign.channels[0]!}`) }) }}</p>
              <details v-for="ch in campaign.channels" :key="ch">
                <summary>{{ t('resend.showOriginal', { channel: t(`channel.inline.${ch}`) }) }}</summary>
                <p v-if="ch === 'sms' && parent.content?.sms" class="original-text">{{ parent.content.sms.body }}</p>
                <template v-else-if="ch === 'email' && parent.content?.email">
                  <p class="original-text"><strong>{{ parent.content.email.subject }}</strong></p>
                  <p class="original-text">{{ parent.content.email.body }}</p>
                </template>
              </details>
            </section>
            <section v-if="uses('sms')" class="card section" aria-labelledby="sms-title">
              <h2 id="sms-title" class="section-title">{{ t('channel.sms') }}</h2>
              <MessageField
                id="sms-body"
                v-model="draft.sms.body"
                :label="t('content.body')"
                multiline
                :rows="4"
                :limit="LIMITS.sms"
                :length="len(shown('sms.body', draft.sms.body))"
                :readonly="streaming || locked"
                :errors="[...(fieldErrors['sms'] ?? []), ...(fieldErrors['sms.body'] ?? [])]"
                variables
                @commit="commit"
              />
              <p v-if="over?.sms" class="estimate">
                <span class="tag">{{ t('content.estimate') }}</span> <strong>{{ t('content.overSms', { count: formatNumber(over.sms) }) }}</strong>
                — {{ t('content.overHint') }}
              </p>
            </section>

            <section v-if="uses('email')" class="card section" aria-labelledby="email-title">
              <h2 id="email-title" class="section-title">{{ t('channel.email') }}</h2>
              <MessageField
                id="email-subject"
                v-model="draft.email.subject"
                :label="t('content.subject')"
                :limit="LIMITS.subject"
                :length="len(shown('email.subject', draft.email.subject))"
                :readonly="streaming || locked"
                :errors="[...(fieldErrors['email'] ?? []), ...(fieldErrors['email.subject'] ?? [])]"
                variables
                @commit="commit"
              />
              <MessageField
                id="email-body"
                v-model="draft.email.body"
                :label="t('content.body')"
                multiline
                :rows="8"
                :limit="LIMITS.body"
                :length="len(shown('email.body', draft.email.body))"
                :readonly="streaming || locked"
                :errors="fieldErrors['email.body']"
                variables
                @commit="commit"
              />
              <p v-if="over?.email" class="estimate">
                <span class="tag">{{ t('content.estimate') }}</span> <strong>{{ t('content.overEmail', { count: formatNumber(over.email) }) }}</strong>
                — {{ t('content.overHint') }}
              </p>
            </section>
            <p v-if="incomplete" class="muted" role="status">{{ t('content.incomplete') }}</p>
          </div>
          <aside class="side">
            <MessagePreview :content="previewContent" :channels="channels" />
          </aside>
        </div>

        <div class="footer">
          <RouterLink :to="{ name: 'campaign', params: { id } }" class="btn">{{ t('content.back') }}</RouterLink>
          <span class="next">
            <span class="muted">{{ t('content.nothingSent') }}</span>
            <RouterLink :to="{ name: 'campaign-review', params: { id } }" class="btn btn-primary btn-lg">{{ t('content.next') }}</RouterLink>
          </span>
        </div>
      </template>
    </template>

    <ConfirmDialog
      :open="regenerateConfirm"
      :title="t('content.regenerateTitle')"
      :confirm-label="t('content.regenerateAction')"
      @confirm="write"
      @close="regenerateConfirm = false"
    >
      <p>{{ t('content.regenerateBody') }}</p>
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
}
.save-state,
.muted {
  font-size: 12px;
  color: var(--muted);
  margin: 0;
}
.audience-line {
  margin: 0;
  font-size: 13px;
  color: var(--ink-2);
}
.ai-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--surface);
  font-size: 13px;
  color: var(--ink-2);
}
.ai-bar.busy {
  border-color: #c9c6f3;
  background: var(--primary-soft);
}
.ai-bar .problem {
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
.section-title {
  margin: 0;
  font-size: 16px;
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
.tag {
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
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
  align-items: center;
  gap: 14px;
}
.original p {
  margin: 0;
}
.original summary {
  cursor: pointer;
  color: var(--link);
  font-weight: 600;
  font-size: 14px;
}
.original-text {
  margin-top: 8px;
  white-space: pre-wrap;
  font-size: 14px;
  color: var(--ink-2);
}
</style>
