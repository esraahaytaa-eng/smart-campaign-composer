<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { ApiError } from '../api'
import { channelsKeepingText } from '../campaign/channels'
import { campaigns, loadCatalog, streamContent, type Estimate } from '../campaign/client'
import { createSaver } from '../campaign/saver'
import { briefTitle } from '../campaign/title'
import { LEGACY, type Campaign, type Catalog, type Channel, type Content, type Lang } from '../campaign/types'
import { render, samplesFor } from '../content/preview'
import { fromContent, fromVariants, generationOutcome, hasUnsavedChanges, languagesToReplace, toSave, type Draft } from '../content/editing'
import { autoWrite, chosenDefault, chosenLanguages, languageName, orderLanguages, startingDefault, toWrite } from '../content/languages'
import { copyAToB, missingB, toSaveB } from '../content/variants'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import MessageField from '../components/MessageField.vue'
import MessagePreview from '../components/MessagePreview.vue'
import StepBar from '../components/StepBar.vue'
import { formatNumber, t, type Key } from '../i18n'
import { session } from '../session'
import { contentAIError, errorText } from '../i18n/errors'

const LIMITS = { sms: 160, subject: 80, body: 2000 } // after personalization (D2)

const route = useRoute()
const router = useRouter()
const id = Number(route.params.id) // each campaign gets its own page instance

const campaign = ref<Campaign | null>(null)
const parent = ref<Campaign | null>(null)
const catalog = ref<Catalog['languages']>([])
const estimate = ref<Estimate | null>(null)
const loadError = ref<unknown>(null)
const saveError = ref<unknown>(null)
const notice = ref<{ key: Key; name?: string } | null>(null)
// One draft per language (D64); the tab shown edits drafts[active].
const drafts = ref<Record<Lang, Draft>>({})
// An A/B test's version B per language (D68), and which version the tab edits.
const draftsB = ref<Record<Lang, Draft>>({})
const showB = ref(false)
const defaultLanguage = ref<Lang>('')
const active = ref<Lang>('')
const fieldErrors = ref<Record<string, string[]>>({})
const incomplete = ref(false)
let chosen: Lang[] = [] // ticked on the audience step (D71), from the URL
// This page's own query: route is the current route, which changes if the marketer leaves while it loads.
const query = { ...route.query }
let gone = false
onBeforeUnmount(() => (gone = true))

const saver = createSaver<Campaign>(
  () => campaign.value?.version ?? 0,
  (c) => (campaign.value = c),
)
const locked = computed(() => saver.state.value === 'conflict')
const isDraft = computed(() => campaign.value?.status === 'draft')
const channels = computed<Channel[]>(() => campaign.value?.channels ?? [])
const uses = (c: Channel) => channels.value.includes(c)
const saved = computed(() => campaign.value?.content ?? null)
const abTest = computed(() => !!campaign.value?.ab_test)
const savedB = computed(() => campaign.value?.content_b ?? null)
const editingB = computed(() => abTest.value && showB.value)
const LETTERS = ['A', 'B'] as const // i18n-ignore: version names, the same in every language
const letter = computed(() => LETTERS[editingB.value ? 1 : 0])
const name = (l: Lang) => languageName(l, catalog.value)
const tabs = computed(() => orderLanguages(Object.keys(drafts.value), defaultLanguage.value, catalog.value))
const draft = computed(() => (editingB.value ? draftsB.value[active.value] : drafts.value[active.value]) ?? fromContent(null))

// Without saved text, the languages chosen on the audience step (D71), else
// the ones the audience suggests, are pre-ticked;
// they are a suggestion, never a restriction (D64). only: after the AI wrote
// these languages, take just their text, so edits elsewhere are kept.
function fromCampaign(c: Campaign, only?: Lang[], variant: 'a' | 'b' = 'a') {
  const fresh = fromVariants(variant === 'b' ? (c.content_b ?? null) : c.content)
  const target = variant === 'b' ? draftsB : drafts
  if (only) for (const l of only) target.value[l] = fresh[l] ?? fromContent(null)
  else {
    drafts.value = fresh
    draftsB.value = fromVariants(c.content_b ?? null)
    if (!c.content) for (const l of chosen.length ? chosen : (c.recommendation?.languages ?? [])) drafts.value[l] = fromContent(null)
  }
  defaultLanguage.value = startingDefault(c.default_language, chosenDefault(chosen, c.recommendation?.default), catalog.value)
  drafts.value[defaultLanguage.value] ??= fromContent(null)
  if (!drafts.value[active.value]) active.value = defaultLanguage.value
  withB()
}

// Every language has a version B draft to edit while the test is on.
function withB() {
  if (abTest.value) for (const l of Object.keys(drafts.value)) draftsB.value[l] ??= fromContent(null)
}

let estimateSeq = 0 // only the latest estimate is shown: saves can finish close together
async function refreshEstimate() {
  const mine = ++estimateSeq
  const e = await campaigns.estimate(id).catch(() => estimate.value)
  if (mine === estimateSeq) estimate.value = e
}

async function load() {
  try {
    const [c, cat] = await Promise.all([campaigns.get(id), loadCatalog()])
    catalog.value = cat.languages
    campaign.value = c
    chosen = chosenLanguages(query.languages, cat.languages)
    fromCampaign(c)
    // A resend shows its original message for reference (D48).
    if (c.parent_campaign_id) campaigns.get(c.parent_campaign_id).then((p) => (parent.value = p), () => {})
    await refreshEstimate()
    // The AI starts by itself at most once (D71): the signal leaves the URL
    // first, so reload, Back or Forward never start it again.
    if (gone || query.write === undefined) return
    const langs = autoWrite(query, c, cat.languages)
    await router.replace({ name: 'campaign-content', params: { id }, query: { ...query, write: undefined } })
    if (langs.length && !gone) write(langs, false)
  } catch (err) {
    loadError.value = err
  }
}
load()

// The content to save: the chosen channels of every complete language.
const content = () => toSave(drafts.value, saved.value, channels.value, defaultLanguage.value)
// Version B to save, for the languages version A keeps; undefined without a test.
const contentB = () => (abTest.value ? toSaveB(draftsB.value, savedB.value, channels.value, Object.keys(content() ?? {})) : undefined)
const complete = () => content() !== null && contentB() !== null
const unsavedB = () => {
  if (!abTest.value) return false
  const kept = Object.fromEntries(Object.entries(draftsB.value).filter(([l]) => drafts.value[l]))
  return hasUnsavedChanges(kept, savedB.value, channels.value)
}
const unsaved = () =>
  !!campaign.value &&
  (hasUnsavedChanges(drafts.value, saved.value, channels.value) ||
    unsavedB() ||
    (saved.value !== null && campaign.value.default_language !== defaultLanguage.value))

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
  unsavedGeneration.value = false
  let sent = false
  try {
    // The save may wait in the queue: check again when it runs, so text
    // cleared or saved meanwhile is never sent.
    await saver.save(async (v) => {
      const c = content()
      const b = contentB()
      if (!c || b === null || !unsaved()) return campaign.value!
      sent = true
      return campaigns.setContent(id, v, c, defaultLanguage.value, b)
    })
    if (!sent) {
      if (!complete() && !quiet) incomplete.value = true
      return !unsaved()
    }
    fieldErrors.value = {}
    notice.value = { key: 'content.saved' }
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
  [drafts, draftsB],
  () => {
    clearTimeout(pause)
    pause = setTimeout(() => commit(true), 1500)
  },
  { deep: true },
)

// ---- Languages (D64, D66) ----
const states = computed(() => campaign.value?.languages ?? {})
type LangState = 'approved' | 'outdated' | 'needsReview' | 'notWritten'
function stateOf(l: Lang): LangState {
  const s = states.value[l]
  if (!saved.value?.[l] || !s) return 'notWritten'
  if (s.approved_at) return 'approved'
  return s.outdated ? 'outdated' : 'needsReview'
}
const removeConfirm = ref<Lang | null>(null)

function share(l: Lang): string {
  const total = campaign.value?.counts?.total ?? 0
  const n = campaign.value?.counts?.by_language?.[l]?.total ?? 0
  return total > 0 ? t('content.share', { percent: `${Math.round((n / total) * 100)}%` }) : ''
}
const recommended = (l: Lang) => campaign.value?.recommendation?.languages.includes(l) ?? false

// The boxes are bound one way: a change the page doesn't make is undone on the box itself.
function toggle(l: Lang, box: HTMLInputElement) {
  if (box.checked) {
    drafts.value[l] ??= fromContent(null)
    withB()
    active.value = l
  } else if (saved.value?.[l]) {
    box.checked = true // until the removal is confirmed
    removeConfirm.value = l
  } else remove(l)
}

function remove(l: Lang) {
  removeConfirm.value = null
  delete drafts.value[l]
  delete draftsB.value[l]
  if (active.value === l) active.value = defaultLanguage.value
  commit()
}

function makeDefault(l: Lang) {
  defaultLanguage.value = l
  commit()
}

// Text from before languages were recorded gets a real language (D64).
function setLanguage(to: Lang) {
  if (!to || drafts.value[to]) return
  drafts.value[to] = drafts.value[LEGACY]!
  delete drafts.value[LEGACY]
  if (defaultLanguage.value === LEGACY) defaultLanguage.value = to
  active.value = to
  commit()
}

// Turning the A/B test on or off saves edits first; version B's text is
// kept while it is off (D68).
async function setAB(box: HTMLInputElement) {
  const on = box.checked
  try {
    if (!(await commit())) return
    saveError.value = null
    await saver.save((v) => campaigns.setAB(id, v, on))
    withB()
    showB.value = on && showB.value
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return
    if (saver.state.value !== 'conflict') saveError.value = err
  } finally {
    box.checked = abTest.value // also when the save failed or wasn't made
  }
}

function copyA() {
  draftsB.value[active.value] = copyAToB(drafts.value[active.value] ?? fromContent(null))
}

// The marketer approves each language's text as it is: saved first, never
// rewritten, no AI call (D66).
async function approve(l: Lang) {
  if (!(await commit())) return
  saveError.value = null
  try {
    await saver.save((v) => campaigns.approveLanguage(id, l, v))
    notice.value = { key: 'content.lang.approved.notice', name: name(l) }
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return
    if (saver.state.value !== 'conflict') saveError.value = err
  }
}

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
// A stopped or failed generation saves nothing (D34), so the header must not say "Saved".
const unsavedGeneration = ref(false)
const retrying = ref(false)
const writing = ref<Lang[]>([])
const live = ref<Record<string, string>>({}) // by field, e.g. "en.sms.body"
const aiProblem = ref<string | null>(null) // the stream's own error text (English) as details
const aiProblemCode = ref<unknown>(null) // the error's code: ai_unavailable or ai_invalid
const aiProblemText = computed(() => contentAIError(aiProblemCode.value))
let stop: AbortController | null = null
const regenerateConfirm = ref<Lang[] | null>(null)

// Version B is written only for languages with saved version A and no B yet.
const missing = computed(() =>
  editingB.value
    ? missingB(Object.keys(saved.value ?? {}), savedB.value).filter((l) => !languagesToReplace([l], draftsB.value, null, channels.value).length)
    : toWrite(tabs.value, saved.value),
)
const writingB = ref(false)

// Ask before the AI replaces anything: saved text or text not saved yet (D66).
function askToWrite(langs: Lang[]) {
  const [d, s] = editingB.value ? [draftsB.value, savedB.value] : [drafts.value, saved.value]
  if (languagesToReplace(langs, d, s, channels.value).length) regenerateConfirm.value = langs
  else write(langs, false)
}

let starting = false // saving the other edits first; streaming is set after
async function write(langs: Lang[], replace: boolean) {
  if (starting || streaming.value) return // a double click starts one generation
  const b = editingB.value
  regenerateConfirm.value = null
  // Other languages' edits are saved first, so they survive the AI's save.
  starting = true
  try {
    await commit(true)
  } finally {
    starting = false
  }
  aiProblem.value = null
  aiProblemCode.value = null
  saveError.value = null
  notice.value = null
  live.value = {}
  retrying.value = false
  writing.value = langs
  writingB.value = b
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
          b ? { languages: langs, replace, variant: 'b' } : { languages: langs, default_language: defaultLanguage.value, replace },
          (e) => {
            const data = e.data as Record<string, unknown>
            if (e.event === 'delta') live.value[data.field as string] = (live.value[data.field as string] ?? '') + (data.text as string)
            else if (e.event === 'retry') {
              retrying.value = true
              live.value = {}
            } else if (e.event === 'done') finished = true
            else if (e.event === 'error') {
              aiProblem.value = String(data.message ?? '')
              aiProblemCode.value = data.code
            }
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
    unsavedGeneration.value = result.outcome !== 'done'
    if (result.outcome === 'done') {
      fromCampaign(campaign.value!, langs, b ? 'b' : 'a')
      notice.value = { key: 'content.done' }
      fieldErrors.value = {}
      await refreshEstimate()
    } else if (result.outcome === 'stopped') notice.value = { key: 'content.stopped' }
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

type Field = 'sms.body' | 'email.subject' | 'email.body'
// While the AI writes this tab's language, the fields show its text so far.
const shown = (field: Field, value: string) =>
  streaming.value && writing.value.includes(active.value) && writingB.value === editingB.value ? (live.value[`${active.value}.${field}`] ?? '') : value
// Version B's errors are prefixed "b." (D68).
const fieldKey = (f: string) => `${editingB.value ? 'b.' : ''}${active.value}.${f}` // i18n-ignore: API field names
const errorsFor = (...fields: string[]) => fields.flatMap((f) => fieldErrors.value[fieldKey(f)] ?? [])
// Errors that belong to no field on this tab: another language, or the whole set.
const otherErrors = computed(() =>
  Object.entries(fieldErrors.value)
    .filter(([f]) => !['sms', 'sms.body', 'email', 'email.subject', 'email.body'].some((x) => f === fieldKey(x)))
    .flatMap(([f, msgs]) => {
      const b = f.startsWith('b.') // i18n-ignore: API field names
      const l = f.split('.')[b ? 1 : 0]!
      const label = b ? `${name(l)} · ${t('ab.version', { v: LETTERS[1] })}` : name(l)
      return msgs.map((m) => (drafts.value[l] || l === LEGACY ? `${label}: ${m}` : m))
    }),
)
const samples = computed(() => samplesFor(active.value))
const len = (text: string) => [...render(text, samples.value.find((s) => s.id === 'long')!.vars)].length
const previewContent = computed<Content>(() => ({
  sms: { body: shown('sms.body', draft.value.sms.body) },
  email: { subject: shown('email.subject', draft.value.email.subject), body: shown('email.body', draft.value.email.body) },
}))
const over = computed(() => (editingB.value ? estimate.value?.over_limit_b : estimate.value?.over_limit)?.by_language?.[active.value] ?? null)
const kept = computed(() => channelsKeepingText(['sms', 'email'], channels.value, saved.value))
// A resend's original, in the language shown, else the original's default.
const original = computed(() => parent.value?.content?.[active.value] ?? parent.value?.content?.[parent.value.default_language ?? ''] ?? null)
const names = (langs: Lang[]) => langs.map(name).join(', ')
const statusText = computed(() => {
  if (streaming.value) return t(retrying.value ? 'content.retrying' : 'content.writing')
  if (aiProblem.value) return aiProblemText.value ?? t('content.aiError')
  return notice.value ? t(notice.value.key, { name: notice.value.name ?? '' }) : ''
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
        <span class="save-state">{{ saver.state.value === 'saving' && !streaming ? t('save.saving') : saver.state.value === 'saved' && !unsavedGeneration ? t('save.saved') : '' }}</span>
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
        <div v-if="otherErrors.length" role="alert" class="alert alert-error">
          <p v-for="m in otherErrors" :key="m">{{ m }}</p>
        </div>

        <fieldset v-if="!drafts[LEGACY]" class="card section languages" :disabled="streaming || locked">
          <legend class="section-title">{{ t('content.languages') }}</legend>
          <p class="muted">{{ t('content.languagesHint') }} {{ t('content.languagesCost') }}</p>
          <div class="language-options">
            <label v-for="l in catalog" :key="l.code" class="language-option">
              <input
                type="checkbox"
                :checked="!!drafts[l.code]"
                :disabled="l.code === defaultLanguage"
                @change="toggle(l.code, $event.target as HTMLInputElement)"
              />
              <span>{{ l.name }}</span>
              <span v-if="share(l.code)" class="muted">{{ share(l.code) }}</span>
              <span v-if="recommended(l.code)" class="tag">{{ t('content.recommended') }}</span>
            </label>
          </div>
          <template v-if="!campaign.parent_campaign_id">
            <label class="language-option ab-switch">
              <input type="checkbox" :checked="abTest" @change="setAB($event.target as HTMLInputElement)" />
              <span>{{ t('ab.switch') }}</span>
            </label>
            <p class="muted">{{ t('ab.switchHint') }}</p>
          </template>
        </fieldset>

        <div v-if="streaming || aiProblem || notice?.key === 'content.stopped' || notice?.key === 'content.done' || missing.length" class="ai-bar" :class="{ busy: streaming }">
          <span v-if="streaming">
            <strong>{{ t(retrying ? 'content.retrying' : 'content.writing') }}</strong> {{ t('content.writingHint') }}
          </span>
          <span v-else-if="aiProblem" class="problem">
            <template v-if="aiProblemText">{{ aiProblemText }}</template>
            <template v-else>{{ t('content.aiError') }} {{ t('error.details', { message: aiProblem }) }}</template>
          </span>
          <span v-else-if="notice?.key === 'content.stopped' || notice?.key === 'content.done'">{{ t(notice.key) }}</span>
          <span v-else-if="missing.length && editingB">{{ t('ab.writesB', { names: names(missing) }) }}</span>
          <span v-else-if="missing.length">{{ !campaign.content ? t('content.empty') : '' }} {{ t('content.writesLanguages', { names: names(missing) }) }}</span>
          <span v-else />
          <button v-if="streaming" type="button" class="btn btn-sm" @click="stop?.abort()">{{ t('content.stop') }}</button>
          <button v-else-if="missing.length" type="button" class="btn btn-sm btn-soft" :disabled="locked" @click="askToWrite(missing)">
            {{ t(editingB ? 'ab.generateB' : 'content.generate') }}
          </button>
        </div>

        <div class="tabs" role="group" :aria-label="t('content.languages')">
          <button
            v-for="l in tabs"
            :key="l"
            type="button"
            class="tab"
            :class="[{ current: l === active }, stateOf(l)]"
            :aria-pressed="l === active"
            @click="active = l"
          >
            {{ name(l) }}
            <span v-if="l === defaultLanguage" class="tag">{{ t('content.lang.default') }}</span>
            <span class="state">{{ t(`content.lang.${stateOf(l)}`) }}</span>
            <span v-if="abTest && saved?.[l] && !savedB?.[l]" class="tag">{{ t('ab.missingB') }}</span>
          </button>
        </div>

        <div class="columns">
          <div class="main">
            <section class="card section lang-panel" :aria-label="name(active)">
              <div v-if="abTest" class="tabs versions" role="group" :aria-label="t('ab.report.version')">
                <button v-for="(v, i) in LETTERS" :key="v" type="button" class="tab" :class="{ current: (i === 1) === showB }" :aria-pressed="(i === 1) === showB" @click="showB = i === 1">
                  {{ t('ab.version', { v }) }}
                </button>
              </div>
              <p v-if="stateOf(active) === 'outdated'" role="alert" class="alert alert-warning">{{ t('content.lang.outdatedHint') }}</p>
              <p v-if="active === defaultLanguage" class="muted">{{ t('content.lang.defaultHint') }}</p>
              <template v-if="active === LEGACY">
                <p class="muted">{{ t('content.lang.legacyHint') }}</p>
                <label class="set-language">
                  {{ t('content.lang.setLanguage') }}
                  <select :disabled="streaming || locked" @change="setLanguage(($event.target as HTMLSelectElement).value)">
                    <option value="">{{ t('content.lang.chooseLanguage') }}</option>
                    <option v-for="l in catalog.filter((c) => !drafts[c.code])" :key="l.code" :value="l.code">{{ l.name }}</option>
                  </select>
                </label>
              </template>
              <div class="lang-actions">
                <button
                  v-if="saved?.[active] && stateOf(active) !== 'approved'"
                  type="button"
                  class="btn btn-sm btn-primary"
                  :disabled="streaming || locked || (abTest && !savedB?.[active])"
                  @click="approve(active)"
                >
                  {{ t('content.lang.approve', { name: name(active) }) }}
                </button>
                <button v-if="!editingB && active !== LEGACY && saved?.[active]" type="button" class="btn btn-sm btn-soft" :disabled="streaming || locked" @click="askToWrite([active])">
                  {{ t('content.lang.rewrite', { name: name(active) }) }}
                </button>
                <button v-if="editingB && savedB?.[active]" type="button" class="btn btn-sm btn-soft" :disabled="streaming || locked" @click="askToWrite([active])">
                  {{ t('ab.rewriteB', { name: name(active) }) }}
                </button>
                <button v-if="editingB" type="button" class="btn btn-sm" :disabled="streaming || locked" @click="copyA">{{ t('ab.copyA') }}</button>
                <button
                  v-if="active !== defaultLanguage && active !== LEGACY && saved?.[active]"
                  type="button"
                  class="btn btn-sm"
                  :disabled="streaming || locked"
                  @click="makeDefault(active)"
                >
                  {{ t('content.lang.makeDefault') }}
                </button>
              </div>
              <p v-if="abTest && saved?.[active] && !savedB?.[active]" class="muted">{{ t('ab.writeBFirst') }}</p>
              <p v-else-if="abTest" class="muted">{{ t('ab.approveCovers', { name: name(active) }) }}</p>
            </section>
            <section v-if="parent && original" class="card section original">
              <p>{{ t('resend.contentLead', { title: briefTitle(parent.request_text), channel: t(`channel.inline.${campaign.channels[0]!}`) }) }}</p>
              <details v-for="ch in campaign.channels" :key="ch">
                <summary>{{ t('resend.showOriginal', { channel: t(`channel.inline.${ch}`) }) }}</summary>
                <p v-if="ch === 'sms' && original.sms" class="original-text">{{ original.sms.body }}</p>
                <template v-else-if="ch === 'email' && original.email">
                  <p class="original-text"><strong>{{ original.email.subject }}</strong></p>
                  <p class="original-text">{{ original.email.body }}</p>
                </template>
              </details>
            </section>
            <section v-if="uses('sms')" class="card section" aria-labelledby="sms-title">
              <h2 id="sms-title" class="section-title">{{ t('channel.sms') }} · {{ name(active) }}<template v-if="abTest"> · {{ t('ab.version', { v: letter }) }}</template></h2>
              <MessageField
                id="sms-body"
                v-model="draft.sms.body"
                :label="t('content.body')"
                multiline
                :rows="4"
                :limit="LIMITS.sms"
                :length="len(shown('sms.body', draft.sms.body))"
                :readonly="streaming || locked"
                :errors="errorsFor('sms', 'sms.body')"
                variables
                @commit="commit"
              />
              <p v-if="over?.sms" class="estimate">
                <span class="tag">{{ t('content.estimate') }}</span> <strong>{{ t('content.overSms', { count: formatNumber(over.sms) }) }}</strong>
                — {{ t('content.overHint') }}
              </p>
            </section>

            <section v-if="uses('email')" class="card section" aria-labelledby="email-title">
              <h2 id="email-title" class="section-title">{{ t('channel.email') }} · {{ name(active) }}<template v-if="abTest"> · {{ t('ab.version', { v: letter }) }}</template></h2>
              <MessageField
                id="email-subject"
                v-model="draft.email.subject"
                :label="t('content.subject')"
                :limit="LIMITS.subject"
                :length="len(shown('email.subject', draft.email.subject))"
                :readonly="streaming || locked"
                :errors="errorsFor('email', 'email.subject')"
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
                :errors="errorsFor('email.body')"
                variables
                @commit="commit"
              />
              <p v-if="over?.email" class="estimate">
                <span class="tag">{{ t('content.estimate') }}</span> <strong>{{ t('content.overEmail', { count: formatNumber(over.email) }) }}</strong>
                — {{ t('content.overHint') }}
              </p>
            </section>
            <p v-for="c in kept" :key="c" class="muted">{{ t('content.keptChannel', { channel: t(`channel.inline.${c}`) }) }}</p>
            <p v-if="incomplete" class="muted" role="status">{{ t('content.incomplete') }}</p>
          </div>
          <aside class="side">
            <MessagePreview :content="previewContent" :channels="channels" :samples="samples" />
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
      :open="regenerateConfirm !== null"
      :title="t('content.regenerateTitle', { names: names(regenerateConfirm ?? []) })"
      :confirm-label="t('content.regenerateAction')"
      @confirm="write(regenerateConfirm ?? [], true)"
      @close="regenerateConfirm = null"
    >
      <p>{{ t('content.regenerateBody', { names: names(regenerateConfirm ?? []) }) }}</p>
    </ConfirmDialog>
    <ConfirmDialog
      :open="removeConfirm !== null"
      :title="t('content.removeTitle', { name: removeConfirm ? name(removeConfirm) : '' })"
      :confirm-label="t('content.removeAction')"
      @confirm="remove(removeConfirm!)"
      @close="removeConfirm = null"
    >
      <p>{{ t('content.removeBody') }}</p>
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
.languages {
  border: 1px solid var(--line);
  margin: 0;
}
.languages legend {
  padding: 0 4px;
}
.language-options {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 20px;
}
.language-option {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 40px;
  font-size: 14px;
}
.tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.tab {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
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
.tab .state {
  font-size: 12px;
  color: var(--muted);
}
.tab.outdated .state {
  color: #5a3a0a;
}
.versions {
  margin: -4px 0 4px;
}
.lang-panel p {
  margin: 0;
}
.lang-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.set-language {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 14px;
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
