<script setup lang="ts">
import type { Status } from '../campaign/types'
import { t } from '../i18n'

defineProps<{ status: Status }>()
</script>

<template>
  <span class="badge" :class="status">{{ t(`status.${status}`) }}</span>
</template>

<style scoped>
.badge {
  display: inline-block;
  font-size: 12px;
  font-weight: 600;
  padding: 3px 9px;
  border-radius: 999px;
  white-space: nowrap;
}
.draft { background: var(--status-draft-bg); color: var(--status-draft-ink); }
.approved { background: var(--status-approved-bg); color: var(--status-approved-ink); }
.sending { background: var(--status-sending-bg); color: var(--status-sending-ink); }
.completed { background: var(--status-completed-bg); color: var(--status-completed-ink); }
.cancelled { background: var(--status-cancelled-bg); color: var(--status-cancelled-ink); }
</style>
