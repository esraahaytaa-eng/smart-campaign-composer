<script setup lang="ts">
import { t } from '../i18n'
</script>

<template>
  <div class="page">
    <h1>{{ t('page.campaigns') }}</h1>
    <div class="card empty">
      <p>{{ t('campaigns.placeholder') }}</p>
    </div>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.empty p {
  margin: 0;
  color: var(--ink-2);
}
</style>
