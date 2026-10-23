<script setup lang="ts">
defineProps<{ inverted?: boolean }>()
</script>

<template>
  <span class="mark" :class="{ inverted }" aria-hidden="true">
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
      <path d="M4 6h16M4 12h10M4 18h6" />
      <circle cx="18" cy="17" r="3" />
    </svg>
  </span>
</template>

<style scoped>
.mark {
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  border-radius: 9px;
  background: var(--ink);
  color: #fff;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.mark.inverted {
  background: #fff;
  color: var(--ink);
}
</style>
