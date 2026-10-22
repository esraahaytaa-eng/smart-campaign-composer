<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { browserTimeZone, campaigns } from '../campaign/client'
import type { Channel } from '../campaign/types'
import ChannelPicker from '../components/ChannelPicker.vue'
import StepBar from '../components/StepBar.vue'
import { t } from '../i18n'
import { aiReason, errorText } from '../i18n/errors'

const MAX_BRIEF = 1000 // characters, as the backend counts them (D15)

const router = useRouter()
const brief = ref('')
const channels = ref<Channel[]>(['sms', 'email'])
const busy = ref(false)
// What went wrong, kept as data so it shows in the current language. reason is
// AI output (an unsupported brief): shown exactly as written, never translated.
const problem = ref<{ text: () => string; reason: string | null } | null>(null)
const briefInput = ref<HTMLTextAreaElement>()

const length = computed(() => [...brief.value.trim()].length)
const tooLong = computed(() => length.value > MAX_BRIEF)

async function interpret() {
  problem.value = null
  if (length.value === 0 || tooLong.value) {
    const empty = length.value === 0
    problem.value = { text: () => (empty ? t('composer.briefEmpty') : t('composer.briefTooLong', { max: MAX_BRIEF })), reason: null }
    briefInput.value?.focus()
    return
  }
  busy.value = true
  try {
    const c = await campaigns.create(brief.value.trim(), channels.value, browserTimeZone())
    await router.replace({ name: 'campaign', params: { id: c.id } })
  } catch (err) {
    // An unsupported brief keeps the text so it can be rephrased; nothing was saved (D15).
    problem.value = { text: () => errorText(err), reason: aiReason(err) }
    briefInput.value?.focus()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="page">
    <div class="head">
      <RouterLink :to="{ name: 'campaigns' }" class="back" :aria-label="t('composer.back')">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M15 6l-6 6 6 6" /></svg>
      </RouterLink>
      <h1>{{ t('page.newCampaign') }}</h1>
    </div>
    <StepBar :current="1" />

    <form class="card brief" novalidate @submit.prevent="interpret">
      <label for="brief" class="section-title">{{ t('composer.brief') }}</label>
      <p id="brief-help" class="help">{{ t('composer.briefHelp') }}</p>
      <textarea
        id="brief"
        ref="briefInput"
        v-model="brief"
        rows="4"
        class="textarea"
        :placeholder="t('composer.briefPlaceholder')"
        aria-describedby="brief-help brief-count"
        :aria-invalid="tooLong"
        :readonly="busy"
      />
      <span id="brief-count" class="count" :class="{ over: tooLong }">{{ t('composer.count', { count: length, max: MAX_BRIEF }) }}</span>

      <div v-if="problem" role="alert" class="alert" :class="problem.reason ? 'alert-warning' : 'alert-error'">
        <span v-if="problem.reason">
          <strong>{{ t('composer.unsupported') }}</strong>
          {{ t('composer.aiReason') }} <q>{{ problem.reason }}</q>
          {{ t('composer.unsupportedHint') }}
        </span>
        <span v-else>{{ problem.text() }}</span>
      </div>

      <div class="bar">
        <ChannelPicker v-model="channels" :disabled="busy" />
        <button type="submit" class="btn btn-primary" :class="{ busy }" :aria-disabled="busy">
          <span v-if="busy" class="spinner" aria-hidden="true" />
          {{ t(busy ? 'composer.interpreting' : 'composer.interpret') }}
        </button>
      </div>
    </form>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-width: 980px;
}
.head {
  display: flex;
  align-items: center;
  gap: 12px;
}
.back {
  width: 40px;
  height: 40px;
  border-radius: var(--radius);
  border: 1px solid var(--line-strong);
  background: var(--surface);
  color: var(--ink);
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.brief {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.section-title {
  font-size: 16px;
  font-weight: 650;
}
.help {
  margin: 0;
  font-size: 13px;
  color: var(--ink-2);
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
.textarea[aria-invalid='true'] {
  border: 1.5px solid var(--danger-line);
}
.count {
  align-self: flex-end;
  font-size: 12px;
  color: var(--muted);
}
.count.over {
  color: var(--danger);
}
.bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
</style>
