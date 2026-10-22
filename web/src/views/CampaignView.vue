<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../api'
import { channelsLosingContent } from '../campaign/channels'
import { campaigns, loadCatalog } from '../campaign/client'
import { defaultForm, fromForm, ruleErrors, toForm, type RuleForm } from '../campaign/rules'
import { createSaver } from '../campaign/saver'
import { briefTitle } from '../campaign/title'
import type { Campaign, Catalog, Channel, FieldError } from '../campaign/types'
import AudiencePanel from '../components/AudiencePanel.vue'
import ChannelPicker from '../components/ChannelPicker.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import RuleChips from '../components/RuleChips.vue'
import RuleEditor from '../components/RuleEditor.vue'
import StepBar from '../components/StepBar.vue'

const route = useRoute()
const router = useRouter()

const campaign = ref<Campaign | null>(null)
const catalog = ref<Catalog | null>(null)
const loadError = ref('')
const saveError = ref('')
const rulesProblem = ref<string[]>([])
const notice = ref('') // announced politely: what just happened
const change = ref<number | null>(null)
const staleContent = ref(false)

const saver = createSaver<Campaign>(
  () => campaign.value?.version ?? 0,
  (c) => (campaign.value = c),
)
const saveLabel = computed(
  () => ({ idle: '', saving: 'Saving…', saved: 'All changes saved', error: 'Not saved', conflict: 'Changed elsewhere' })[saver.state.value],
)

const id = computed(() => Number(route.params.id))
const isDraft = computed(() => campaign.value?.status === 'draft')
const isResend = computed(() => campaign.value?.parent_campaign_id != null)
const locked = computed(() => saver.state.value === 'conflict')

async function load() {
  loadError.value = ''
  campaign.value = null
  try {
    const [c, cat] = await Promise.all([campaigns.get(id.value), loadCatalog()])
    campaign.value = c
    catalog.value = cat
    brief.value = c.request_text
  } catch (err) {
    loadError.value = err instanceof ApiError && err.status === 404 ? 'This campaign doesn’t exist, or it was deleted.' : err instanceof ApiError ? err.message : 'Something went wrong.'
  }
}
watch(id, load, { immediate: true })

const message = (err: unknown) => (err instanceof ApiError ? err.message : 'Something went wrong. Try again.')

// save runs one change through the queue. A failure shows in the page's
// banner unless the caller shows it where it belongs (banner: false).
async function save(op: (version: number) => Promise<Campaign>, done?: string, banner = true): Promise<void> {
  saveError.value = ''
  try {
    await saver.save(op)
    if (done) notice.value = done
  } catch (err) {
    if (banner && saver.state.value !== 'conflict') saveError.value = message(err)
    throw err
  }
}

// ---- channels ----
const channelConfirm = ref<{ next: Channel[]; losing: Channel[] } | null>(null)
function pickChannels(next: Channel[]) {
  const c = campaign.value!
  const losing = channelsLosingContent(c.channels, next, c.content)
  if (losing.length) channelConfirm.value = { next, losing }
  else applyChannels(next)
}
function applyChannels(next: Channel[]) {
  channelConfirm.value = null
  const before = campaign.value?.counts?.total
  save((v) => campaigns.setChannels(id.value, v, next), 'Channels saved.')
    .then(() => (change.value = before === undefined ? null : (campaign.value?.counts?.total ?? 0) - before))
    .catch(() => {})
}
const channelName = (c: Channel) => (c === 'sms' ? 'SMS' : 'Email')

// ---- rules ----
const editor = ref<{ index: number; form: RuleForm; errors: string[]; saving: boolean } | null>(null)
const ruleErrorMap = ref<Map<number, string[]>>(new Map())

function editRule(index: number) {
  editor.value = { index, form: toForm(campaign.value!.rules![index]!, catalog.value!), errors: [], saving: false }
}
function addRule(field: string, op: string) {
  editor.value = { index: -1, form: defaultForm(field, op, catalog.value!), errors: [], saving: false }
}

async function saveRules(next: NonNullable<Campaign['rules']>, target: number): Promise<string[]> {
  const before = campaign.value?.counts?.total
  rulesProblem.value = []
  try {
    await save((v) => campaigns.setRules(id.value, v, next), 'Audience rules saved.', false)
    ruleErrorMap.value = new Map()
    change.value = before === undefined ? null : (campaign.value?.counts?.total ?? 0) - before
    return []
  } catch (err) {
    if (saver.state.value === 'conflict') return [] // the page banner explains it
    const fieldErrors = err instanceof ApiError ? ((err.body as { field_errors?: FieldError[] })?.field_errors ?? []) : []
    if (fieldErrors.length === 0) return [message(err)]
    const { byRule, general } = ruleErrors(fieldErrors)
    return [...(byRule.get(target) ?? []), ...general]
  }
}

async function applyRule(form: RuleForm) {
  const e = editor.value!
  const rules = [...(campaign.value!.rules ?? [])]
  const index = e.index >= 0 ? e.index : rules.length
  rules[index] = fromForm(form, catalog.value!)
  e.saving = true
  e.errors = await saveRules(rules, index)
  e.saving = false
  if (e.errors.length === 0) editor.value = null
}

async function removeRule(index: number) {
  const rules = campaign.value!.rules!.filter((_, i) => i !== index)
  rulesProblem.value = await saveRules(rules, -1)
}

// ---- brief ----
const brief = ref('')
const briefBusy = ref(false)
const briefProblem = ref<{ text: string; unsupported: boolean } | null>(null)
const reinterpretConfirm = ref(false)
const briefChanged = computed(() => brief.value.trim() !== (campaign.value?.request_text ?? ''))

async function reinterpret() {
  reinterpretConfirm.value = false
  briefProblem.value = null
  briefBusy.value = true
  const before = campaign.value?.counts?.total
  const hadContent = !!campaign.value?.content
  try {
    await saver.save((v) => campaigns.setRequest(id.value, v, brief.value.trim()))
    notice.value = 'The AI read the brief again and replaced the audience rules.'
    change.value = before === undefined ? null : (campaign.value?.counts?.total ?? 0) - before
    staleContent.value = hadContent
  } catch (err) {
    // Nothing was saved (D15); the new text stays so it can be rephrased.
    const unsupported = err instanceof ApiError && (err.body as { unsupported?: boolean } | undefined)?.unsupported === true
    briefProblem.value = saver.state.value === 'conflict' ? null : { text: message(err), unsupported }
  } finally {
    briefBusy.value = false
  }
}

// ---- delete ----
const deleteConfirm = ref(false)
async function deleteDraft() {
  deleteConfirm.value = false
  try {
    await saver.save(async (v) => {
      await campaigns.remove(id.value, v)
      return campaign.value!
    })
    await router.replace({ name: 'campaigns' })
  } catch (err) {
    if (saver.state.value !== 'conflict') saveError.value = message(err)
  }
}

function reload() {
  window.location.reload() // a fresh start also clears the save queue's conflict state
}
</script>

<template>
  <div class="page">
    <div v-if="loadError" role="alert" class="alert alert-error">
      {{ loadError }} <RouterLink :to="{ name: 'campaigns' }">Back to campaigns</RouterLink>
    </div>
    <p v-else-if="!campaign || !catalog" class="loading" role="status">Loading the campaign…</p>

    <template v-else>
      <div class="head">
        <RouterLink :to="{ name: 'campaigns' }" class="back" aria-label="Back to campaigns">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M15 6l-6 6 6 6" /></svg>
        </RouterLink>
        <!-- Campaigns are identified by their brief (D43); the full brief is below. -->
        <h1 class="title">{{ briefTitle(campaign.request_text) }}</h1>
        <span class="status">{{ isDraft ? 'Draft' : campaign.status }}</span>
        <span v-if="isResend" class="status">Resend · {{ channelName(campaign.channels[0]!) }}</span>
        <span class="save-state" :class="saver.state.value">{{ saveLabel }}</span>
        <button v-if="isDraft" type="button" class="btn btn-sm btn-danger-outline" :disabled="locked" @click="deleteConfirm = true">Delete draft…</button>
      </div>

      <p class="sr-only" role="status">{{ notice }}</p>
      <div v-if="locked" role="alert" class="alert alert-warning">
        <span>This draft was changed elsewhere, in another tab or by another marketer. Reload to see the latest version before editing again.</span>
        <button type="button" class="btn btn-sm" @click="reload">Reload</button>
      </div>
      <div v-else-if="saveError" role="alert" class="alert alert-error">{{ saveError }}</div>

      <div v-if="!isDraft" class="alert alert-info">
        This campaign is {{ campaign.status }}, so it can no longer be edited. Its review and results screens are added in later steps of the plan
        (T17–T18).
      </div>

      <template v-else>
        <StepBar :current="1" />
        <div v-if="staleContent" role="alert" class="alert alert-warning">
          The messages were written for the earlier brief and may no longer match it. Review or regenerate them before approval.
        </div>

        <div class="columns">
          <div class="main">
            <section v-if="!isResend" class="card section" aria-labelledby="brief-label">
              <label id="brief-label" for="brief" class="section-title">Campaign brief</label>
              <textarea id="brief" v-model="brief" rows="3" class="textarea" :readonly="briefBusy || locked" aria-describedby="brief-help" />
              <span id="brief-help" class="help">
                {{ briefChanged ? 'Edited. Re-interpret to update the audience rules; until then the rules below still apply.' : 'Re-interpreting replaces the audience rules below.' }}
              </span>
              <div v-if="briefProblem" role="alert" class="alert" :class="briefProblem.unsupported ? 'alert-warning' : 'alert-error'">
                <span>
                  <strong v-if="briefProblem.unsupported">The AI couldn't turn this brief into audience rules.</strong>
                  {{ briefProblem.text }} The draft is unchanged.
                </span>
              </div>
              <div class="bar">
                <ChannelPicker :model-value="campaign.channels" :disabled="locked" @update:model-value="pickChannels" />
                <button type="button" class="btn btn-sm" :class="{ busy: briefBusy }" :aria-disabled="briefBusy || locked" @click="!briefBusy && !locked && (reinterpretConfirm = true)">
                  <span v-if="briefBusy" class="spinner dark" aria-hidden="true" />
                  {{ briefBusy ? 'The AI is reading the brief…' : 'Re-interpret brief' }}
                </button>
              </div>
            </section>

            <section class="card section" aria-labelledby="rules-title">
              <div class="section-head">
                <h2 id="rules-title" class="section-title">Audience rules</h2>
                <span v-if="!isResend" class="help">Suggested by the AI · click a rule to edit it</span>
              </div>
              <p v-if="isResend" class="help">
                A resend goes to the recipients of the original campaign who were skipped because the {{ channelName(campaign.channels[0]!) }} was too
                long. Its audience and channel are fixed.
              </p>
              <template v-else>
                <RuleChips
                  :rules="campaign.rules ?? []"
                  :catalog="catalog"
                  :errors="ruleErrorMap"
                  :disabled="locked || briefBusy"
                  @edit="editRule"
                  @add="addRule"
                  @remove="removeRule"
                />
                <div v-if="rulesProblem.length" role="alert" class="alert alert-error">{{ rulesProblem.join(' ') }}</div>
              </template>
            </section>
          </div>

          <aside class="side">
            <AudiencePanel :counts="campaign.counts" :channels="campaign.channels" :change="change" :resend="isResend" />
          </aside>
        </div>

        <div class="footer">
          <span class="help">Next: the AI writes the {{ campaign.channels.map(channelName).join(' and ') }} message{{ campaign.channels.length > 1 ? 's' : '' }}.</span>
          <span class="next">
            <button type="button" class="btn btn-primary btn-lg" aria-disabled="true" aria-describedby="next-help">Generate content →</button>
            <span id="next-help" class="help">The content step is added in the next step of the plan (T17).</span>
          </span>
        </div>
      </template>
    </template>

    <RuleEditor
      v-if="catalog"
      :open="!!editor"
      :form="editor?.form ?? null"
      :catalog="catalog"
      :server-errors="editor?.errors"
      :saving="editor?.saving"
      @apply="applyRule"
      @close="editor = null"
    />

    <ConfirmDialog
      :open="!!channelConfirm"
      :title="`Remove ${channelConfirm?.losing.map(channelName).join(' and ')}?`"
      confirm-label="Remove and delete content"
      danger
      @confirm="applyChannels(channelConfirm!.next)"
      @close="channelConfirm = null"
    >
      <p>
        The {{ channelConfirm?.losing.map(channelName).join(' and ') }} content written for this campaign will be deleted. Adding the channel again
        later means writing it again.
      </p>
    </ConfirmDialog>

    <ConfirmDialog :open="reinterpretConfirm" title="Re-interpret the brief?" confirm-label="Re-interpret" @confirm="reinterpret" @close="reinterpretConfirm = false">
      <p>The AI reads the brief again and replaces the audience rules and the proposed send time. Rules you edited by hand are lost.</p>
      <p v-if="campaign?.content">The messages are kept, but they were written for the earlier brief: review or regenerate them before approval.</p>
    </ConfirmDialog>

    <ConfirmDialog :open="deleteConfirm" title="Delete draft?" confirm-label="Delete permanently" cancel-label="Keep draft" danger @confirm="deleteDraft" @close="deleteConfirm = false">
      <p><strong>{{ campaign ? briefTitle(campaign.request_text) : '' }}</strong></p>
      <p>The brief, audience rules and messages will be permanently deleted. This can’t be undone. Nothing was sent from this draft.</p>
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
