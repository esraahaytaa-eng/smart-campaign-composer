<script setup lang="ts">
import type { Campaign } from '../campaign/types'
import { briefTitle } from '../campaign/title'
import { formatDateTime, t } from '../i18n'
import ConfirmDialog from './ConfirmDialog.vue'

// Cancelling an approved or sending campaign (D11), asked first. The parent
// calls the API on confirm.
defineProps<{ campaign: Campaign | null }>()
const emit = defineEmits<{ confirm: [Campaign]; close: [] }>()
</script>

<template>
  <ConfirmDialog
    :open="campaign !== null"
    :title="campaign ? t('cancel.title', { title: briefTitle(campaign.request_text) }) : ''"
    :confirm-label="t('cancel.action')"
    :cancel-label="t('cancel.keep')"
    danger
    @confirm="emit('confirm', campaign!)"
    @close="emit('close')"
  >
    <template v-if="campaign?.status === 'approved'">
      <p>{{ t('cancel.scheduled', { time: campaign.send_at ? formatDateTime(campaign.send_at) : '' }) }}</p>
      <p class="recall">{{ t('cancel.scheduledRecall') }}</p>
    </template>
    <template v-else-if="campaign">
      <p>{{ t('cancel.sending') }}</p>
      <p class="recall">{{ t('cancel.sendingRecall') }}</p>
    </template>
  </ConfirmDialog>
</template>

<style scoped>
.recall {
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--notice-soft);
  border: 1px solid var(--notice-line);
  color: var(--notice-ink);
  font-size: 13px;
}
</style>
