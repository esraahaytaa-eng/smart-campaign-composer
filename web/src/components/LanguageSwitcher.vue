<script setup lang="ts">
// The TR/EN switch (D44). Each language is named in its own language and
// marked with lang=, so screen readers pronounce it correctly.
import { locale, locales, setLocale, t } from '../i18n'
</script>

<template>
  <div class="switcher" role="group" :aria-label="t('lang.label')">
    <button
      v-for="l in locales"
      :key="l.code"
      type="button"
      :lang="l.code"
      :aria-pressed="locale === l.code"
      @click="setLocale(l.code)"
    >
      {{ l.name }}
    </button>
  </div>
</template>

<style scoped>
.switcher {
  display: inline-flex;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-sm);
  overflow: hidden;
}
button {
  height: 30px;
  padding: 0 10px;
  border: 0;
  background: var(--surface);
  color: var(--ink-2);
  font: 600 12px var(--font);
  cursor: pointer;
}
button + button {
  border-left: 1px solid var(--line-strong);
}
button[aria-pressed='true'] {
  background: var(--ink);
  color: #fff;
}
</style>
