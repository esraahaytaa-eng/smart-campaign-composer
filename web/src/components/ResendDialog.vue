<script setup lang="ts">
import { DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { campaigns } from '../campaign/client'
import type { Campaign, Channel } from '../campaign/types'
import { resendFunnel, type Snapshot } from '../delivery/status'
import { formatNumber, t } from '../i18n'
import { errorText } from '../i18n/errors'

// Who a resend reaches and who it leaves out (board 6), then a draft
// follow-up that goes through the content and review steps (D31, D48).
const props = defineProps<{ campaign: Campaign; snapshot: Snapshot; channel: Channel | null }>()
const emit = defineEmits<{ close: []; failed: [] }>()

const router = useRouter()
const busy = ref(false)
const problem = ref<unknown>(null)
watch(
  () => props.channel,
  () => (problem.value = null),
)

const funnel = computed(() => (props.channel ? resendFunnel(props.snapshot, props.channel) : null))
const inline = computed(() => (props.channel ? t(`channel.inline.${props.channel}`) : ''))
const other = computed(() => props.campaign.channels.find((ch) => ch !== props.channel))

async function create() {
  if (!props.channel || busy.value) return
  busy.value = true
  problem.value = null
  try {
    const draft = await campaigns.resend(props.campaign.id, props.channel)
    await router.push({ name: 'campaign-content', params: { id: draft.id } })
  } catch (err) {
    // Usually a resend made elsewhere meanwhile: say so, and let the page
    // fetch the current offer.
    problem.value = err
    emit('failed')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <DialogRoot :open="channel !== null" @update:open="(v) => !v && emit('close')">
    <DialogPortal>
      <DialogOverlay class="overlay" />
      <DialogContent v-if="funnel" class="dialog resend">
        <DialogTitle class="title">{{ t('resend.title', { channel: inline }) }}</DialogTitle>
        <DialogDescription class="lead">{{ t('resend.lead', { channel: inline }) }}</DialogDescription>

        <section class="block" aria-labelledby="who-h">
          <h3 id="who-h">{{ t('resend.who') }}</h3>
          <div class="funnel">
            <div class="line"><span>{{ t('resend.skipped') }}</span><strong>{{ formatNumber(funnel.skipped) }}</strong></div>
            <div v-if="funnel.noLonger" class="line"><span>{{ t('resend.noLonger') }}</span><strong>−{{ formatNumber(funnel.noLonger) }}</strong></div>
            <div class="line total">
              <span>{{ t('resend.eligible') }} <span class="tag">{{ t('resend.estimate') }}</span></span>
              <strong>{{ formatNumber(funnel.eligible) }}</strong>
            </div>
          </div>
          <p class="muted">{{ t('resend.recheck') }}</p>
        </section>

        <section class="block" aria-labelledby="not-h">
          <h3 id="not-h">{{ t('resend.notIncluded') }}</h3>
          <div class="excl">
            <span class="icon no" aria-hidden="true">×</span>
            <span><strong>{{ t('resend.consent') }}</strong><span class="muted">{{ t('resend.consentWhy') }}</span></span>
            <strong>{{ formatNumber(funnel.noConsent) }}</strong>
          </div>
          <div class="excl">
            <span class="icon unknown" aria-hidden="true">?</span>
            <span><strong>{{ t('resend.uncertain') }}</strong><span class="muted">{{ t('resend.uncertainWhy') }}</span></span>
            <strong>{{ formatNumber(funnel.uncertain) }}</strong>
          </div>
          <div class="excl">
            <span class="icon" aria-hidden="true">–</span>
            <span><strong>{{ t('resend.failed') }}</strong><span class="muted">{{ t('resend.failedWhy') }}</span></span>
            <strong>{{ formatNumber(funnel.failed) }}</strong>
          </div>
          <p v-if="other" class="muted">{{ t('resend.otherChannel', { channel: t(`channel.inline.${other}`) }) }}</p>
        </section>

        <p class="next">{{ t('resend.next') }}</p>
        <div v-if="problem" role="alert" class="alert alert-error">{{ errorText(problem) }}</div>
        <div class="actions">
          <DialogClose class="btn" type="button">{{ t('resend.back') }}</DialogClose>
          <button type="button" class="btn btn-primary" :class="{ busy }" :aria-disabled="busy || funnel.eligible === 0" :disabled="funnel.eligible === 0" @click="create">
            <span v-if="busy" class="spinner" aria-hidden="true" />
            {{ busy ? t('resend.creating') : t('resend.create') }}
          </button>
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style scoped>
.resend {
  max-width: 560px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.lead,
.muted {
  margin: 0;
  font-size: 13px;
  color: var(--muted);
}
.block {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
}
.funnel {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  overflow: hidden;
}
.line {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 9px 12px;
  font-size: 14px;
}
.line + .line {
  border-top: 1px solid var(--line);
}
.line.total {
  background: var(--surface-muted);
  font-weight: 600;
}
.tag {
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px dashed var(--muted);
  color: var(--ink-2);
}
.excl {
  display: grid;
  grid-template-columns: 24px minmax(0, 1fr) auto;
  gap: 10px;
  align-items: start;
  font-size: 13px;
}
.excl > span:nth-child(2) {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.icon {
  width: 24px;
  height: 24px;
  border-radius: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-weight: 700;
  background: var(--status-cancelled-bg);
  color: var(--status-cancelled-ink);
}
.icon.no {
  background: var(--danger-soft);
  color: var(--danger);
}
.icon.unknown {
  background: var(--group-uncertain-soft);
  color: var(--group-uncertain-ink);
}
.next {
  margin: 0;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--primary-soft);
  color: var(--primary-ink);
  font-size: 13px;
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
