<script setup lang="ts">
import { t } from '../i18n'
</script>

<template>
  <div class="page">
    <h1>{{ t('page.notFound') }}</h1>
    <p>{{ t('notFound.text') }} <RouterLink :to="{ name: 'dashboard' }">{{ t('notFound.link') }}</RouterLink></p>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 12px;
  color: var(--ink-2);
}
</style>
