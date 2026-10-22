<template>
  <div class="page">
    <h1>Page not found</h1>
    <p>This page doesn’t exist. <RouterLink :to="{ name: 'dashboard' }">Go to the dashboard</RouterLink></p>
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
