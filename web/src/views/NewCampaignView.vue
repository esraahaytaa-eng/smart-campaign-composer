<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError } from '../api'
import { browserTimeZone, campaigns } from '../campaign/client'
import type { Channel } from '../campaign/types'
import ChannelPicker from '../components/ChannelPicker.vue'
import StepBar from '../components/StepBar.vue'

const MAX_BRIEF = 1000 // characters, as the backend counts them (D15)

const router = useRouter()
const brief = ref('')
const channels = ref<Channel[]>(['sms', 'email'])
const busy = ref(false)
const problem = ref<{ text: string; unsupported: boolean } | null>(null)
const briefInput = ref<HTMLTextAreaElement>()

const length = computed(() => [...brief.value.trim()].length)
const tooLong = computed(() => length.value > MAX_BRIEF)

async function interpret() {
  problem.value = null
  if (length.value === 0 || tooLong.value) {
    problem.value = { text: length.value === 0 ? 'Write a brief first.' : `Shorten the brief to ${MAX_BRIEF} characters.`, unsupported: false }
    briefInput.value?.focus()
    return
  }
  busy.value = true
  try {
    const c = await campaigns.create(brief.value.trim(), channels.value, browserTimeZone())
    await router.replace({ name: 'campaign', params: { id: c.id } })
  } catch (err) {
    // An unsupported brief keeps the text so it can be rephrased; nothing was saved (D15).
    const unsupported = err instanceof ApiError && (err.body as { unsupported?: boolean } | undefined)?.unsupported === true
    problem.value = { text: err instanceof ApiError ? err.message : 'Something went wrong. Try again.', unsupported }
    briefInput.value?.focus()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="page">
    <div class="head">
      <RouterLink :to="{ name: 'campaigns' }" class="back" aria-label="Back to campaigns">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M15 6l-6 6 6 6" /></svg>
      </RouterLink>
      <h1>New campaign</h1>
    </div>
    <StepBar :current="1" />

    <form class="card brief" novalidate @submit.prevent="interpret">
      <label for="brief" class="section-title">Campaign brief</label>
      <p id="brief-help" class="help">
        Describe who should get the campaign and what it offers. The AI turns it into audience rules you can check and edit.
      </p>
      <textarea
        id="brief"
        ref="briefInput"
        v-model="brief"
        rows="4"
        class="textarea"
        placeholder="e.g. Send a 20% discount reminder to customers in Istanbul who added products to their cart in the last 30 days but did not buy anything."
        aria-describedby="brief-help brief-count"
        :aria-invalid="tooLong"
        :readonly="busy"
      />
      <span id="brief-count" class="count" :class="{ over: tooLong }">{{ length }} / {{ MAX_BRIEF }}</span>

      <div v-if="problem" role="alert" class="alert" :class="problem.unsupported ? 'alert-warning' : 'alert-error'">
        <span>
          <strong v-if="problem.unsupported">The AI couldn't turn this brief into audience rules.</strong>
          {{ problem.text }}
          <template v-if="problem.unsupported"> Rephrase the brief using the conditions the audience rules support.</template>
        </span>
      </div>

      <div class="bar">
        <ChannelPicker v-model="channels" :disabled="busy" />
        <button type="submit" class="btn btn-primary" :class="{ busy }" :aria-disabled="busy">
          <span v-if="busy" class="spinner" aria-hidden="true" />
          {{ busy ? 'The AI is reading the brief…' : 'Interpret with AI' }}
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
