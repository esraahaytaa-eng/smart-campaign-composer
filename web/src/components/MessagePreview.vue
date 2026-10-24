<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Channel, Content } from '../campaign/types'
import { render, SAMPLES, type Sample } from '../content/preview'
import { smsInfo } from '../content/sms'
import { formatNumber, t } from '../i18n'

// The message as one fictional customer would get it (T17, option A).
// Customer and AI text are shown as plain text, never as HTML.
const props = withDefaults(defineProps<{ content: Content; channels: Channel[]; samples?: Sample[] }>(), { samples: () => SAMPLES })
const sampleId = ref<'typical' | 'long'>('typical')
const tab = ref<Channel>(props.channels[0] ?? 'sms')
const sample = computed(() => props.samples.find((s) => s.id === sampleId.value)!)
const sms = computed(() => render(props.content.sms?.body ?? '', sample.value.vars))
const subject = computed(() => render(props.content.email?.subject ?? '', sample.value.vars))
const body = computed(() => render(props.content.email?.body ?? '', sample.value.vars))
const info = computed(() => smsInfo(sms.value))
const name = (s: Sample) => `${s.vars.first_name} ${s.vars.last_name} · ${s.vars.city}`
</script>

<template>
  <section class="card preview" aria-labelledby="preview-title">
    <div class="top">
      <h2 id="preview-title">{{ t('content.preview') }}</h2>
      <div v-if="channels.length > 1" class="tabs" role="group" :aria-label="t('content.preview')">
        <button v-for="c in channels" :key="c" type="button" :aria-pressed="tab === c" @click="tab = c">
          {{ t(c === 'sms' ? 'channel.sms' : 'channel.email') }}
        </button>
      </div>
    </div>
    <div class="samples" role="group" :aria-label="t('content.showAs')">
      <span>{{ t('content.showAs') }}</span>
      <button v-for="s in samples" :key="s.id" type="button" :aria-pressed="sampleId === s.id" @click="sampleId = s.id">
        <strong>{{ t(`content.sample.${s.id}`) }}</strong>
        <span>{{ name(s) }}</span>
      </button>
    </div>

    <template v-if="tab === 'sms' && channels.includes('sms')">
      <p v-if="sms" class="bubble">{{ sms }}</p>
      <p v-else class="empty">{{ t('content.previewEmpty') }}</p>
      <p class="meta">
        <span :class="{ over: [...sms].length > 160 }">{{ t('content.chars', { count: formatNumber([...sms].length), max: formatNumber(160) }) }}</span>
        · {{ t('content.segments', { count: info.segments, encoding: info.encoding }) }}
      </p>
      <p class="hint">{{ t('content.segmentsHint') }}</p>
    </template>
    <template v-else-if="channels.includes('email')">
      <p class="subject"><span>{{ t('content.subject') }} · </span><strong>{{ subject }}</strong></p>
      <p v-if="body" class="mail">{{ body }}</p>
      <p v-else class="empty">{{ t('content.previewEmpty') }}</p>
    </template>
    <p class="note">{{ t('content.fictional') }}</p>
  </section>
</template>

<style scoped>
.preview {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}
h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 650;
}
.tabs,
.samples {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
.samples > span {
  font-size: 12px;
  color: var(--muted);
}
.tabs button,
.samples button {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  padding: 6px 10px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font: 500 12px var(--font);
  color: var(--ink-2);
  cursor: pointer;
}
.tabs button[aria-pressed='true'],
.samples button[aria-pressed='true'] {
  border-color: var(--primary);
  background: var(--primary-soft);
  color: var(--primary-ink);
}
.bubble {
  margin: 0;
  padding: 12px 14px;
  border-radius: 14px 14px 14px 4px;
  background: #efede7;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.subject {
  margin: 0;
  font-size: 13px;
}
.mail {
  margin: 0;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.meta,
.hint,
.note,
.empty {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
}
.meta .over {
  color: var(--danger);
  font-weight: 600;
}
</style>
