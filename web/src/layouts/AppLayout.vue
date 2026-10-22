<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuPortal, DropdownMenuRoot, DropdownMenuTrigger } from 'reka-ui'
import { logout, session } from '../session'
import BrandMark from '../components/BrandMark.vue'

const router = useRouter()

const initials = computed(() =>
  (session.value?.name ?? '')
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase())
    .join(''),
)
// Providers are always mocked; the AI may be Claude (LLM_MODE=real, D24).
const modeLabel = computed(() => (session.value?.llm_mode === 'real' ? 'Claude AI · Mock providers' : 'Mock AI · Mock providers'))
const modeTitle = computed(() =>
  session.value?.llm_mode === 'real'
    ? 'The AI is Claude. SMS and email providers are simulated: no real messages are sent.'
    : 'The AI and the SMS and email providers are simulated. No real messages are sent.',
)

async function signOut() {
  await logout().catch(() => {}) // the session is cleared locally either way
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div class="shell">
    <nav class="sidebar" aria-label="Primary">
      <div class="brand">
        <BrandMark />
        <span>Smart Campaign<br />Composer</span>
      </div>
      <div class="links">
        <RouterLink :to="{ name: 'dashboard' }" class="link" exact-active-class="active">Dashboard</RouterLink>
        <RouterLink :to="{ name: 'campaigns' }" class="link" active-class="active">Campaigns</RouterLink>
      </div>
      <div class="foot">
        <span class="mode-badge" :title="modeTitle">{{ modeLabel }}</span>
        <DropdownMenuRoot>
          <DropdownMenuTrigger class="account" aria-label="Account menu">
            <span class="avatar" aria-hidden="true">{{ initials }}</span>
            <span class="who">
              <span class="name">{{ session?.name }}</span>
              <span class="email">{{ session?.email }}</span>
            </span>
          </DropdownMenuTrigger>
          <DropdownMenuPortal>
            <DropdownMenuContent class="menu" side="top" align="start" :side-offset="8">
              <DropdownMenuLabel class="menu-label">Signed in as<strong>{{ session?.email }}</strong></DropdownMenuLabel>
              <DropdownMenuItem class="menu-item menu-item-danger" @select="signOut">Log out</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenuPortal>
        </DropdownMenuRoot>
      </div>
    </nav>
    <main class="content">
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.shell {
  min-height: 100vh;
  display: flex;
  flex-wrap: wrap;
}
.sidebar {
  flex: 1 1 220px;
  max-width: 260px;
  background: var(--surface);
  border-right: 1px solid var(--line);
  padding: 22px 16px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 6px;
  font-weight: 650;
  line-height: 1.2;
}
.links {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.link {
  padding: 10px;
  border-radius: var(--radius-sm);
  color: var(--ink-2);
  font-weight: 500;
  text-decoration: none;
}
.link:hover {
  background: var(--surface-muted);
}
.link.active {
  background: var(--primary-soft);
  color: var(--primary-ink);
  font-weight: 600;
}
.foot {
  margin-top: auto;
  display: flex;
  flex-direction: column;
  gap: 14px;
  align-items: flex-start;
}
.account {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 4px;
  border: 0;
  border-radius: var(--radius);
  background: transparent;
  cursor: pointer;
  text-align: left;
  font-family: var(--font);
  color: var(--ink);
}
.account[data-state='open'],
.account:hover {
  background: var(--primary-soft);
}
.avatar {
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  border-radius: 999px;
  background: #e3e1f9;
  color: var(--primary-ink);
  font-size: 13px;
  font-weight: 700;
  display: flex;
  align-items: center;
  justify-content: center;
}
.who {
  display: flex;
  flex-direction: column;
  min-width: 0;
  font-size: 13px;
}
.name {
  font-weight: 600;
}
.email {
  color: var(--muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.content {
  flex: 999 1 560px;
  min-width: 0;
  padding: 24px 32px 40px;
}
@media (max-width: 720px) {
  .sidebar {
    max-width: none;
    border-right: 0;
    border-bottom: 1px solid var(--line);
  }
  .content {
    padding: 20px 16px 32px;
  }
}
</style>
