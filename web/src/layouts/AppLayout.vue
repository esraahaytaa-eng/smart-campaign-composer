<script setup lang="ts">
import { computed } from 'vue'
import { useRouter, isNavigationFailure, NavigationFailureType } from 'vue-router'
import { DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuPortal, DropdownMenuRoot, DropdownMenuTrigger } from 'reka-ui'
import { logout, session } from '../session'
import BrandMark from '../components/BrandMark.vue'
import LanguageSwitcher from '../components/LanguageSwitcher.vue'
import { t } from '../i18n'

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
const modeLabel = computed(() => t(session.value?.llm_mode === 'real' ? 'mode.real' : 'mode.mock'))
const modeTitle = computed(() => t(session.value?.llm_mode === 'real' ? 'mode.realHelp' : 'mode.mockHelp'))

// Leave the page first, so its guard can save unsaved text or ask; signing
// out first would leave nothing able to save it. A blocked leave keeps the session.
async function signOut() {
  const failure = await router.push({ name: 'dashboard' })
  if (isNavigationFailure(failure, NavigationFailureType.aborted)) return
  await logout().catch(() => {}) // the session is cleared locally either way
  await router.replace({ name: 'login' })
}
</script>

<template>
  <div class="shell">
    <nav class="sidebar" :aria-label="t('nav.primary')">
      <div class="brand">
        <BrandMark />
        <span class="brand-name">{{ t('app.name') }}</span>
      </div>
      <RouterLink :to="{ name: 'new-campaign' }" class="new">{{ t('nav.newCampaign') }}</RouterLink>
      <div class="links">
        <RouterLink :to="{ name: 'dashboard' }" class="link" exact-active-class="active">{{ t('page.dashboard') }}</RouterLink>
        <RouterLink :to="{ name: 'campaigns' }" class="link" :class="{ active: $route.path.startsWith('/campaigns') }">{{ t('page.campaigns') }}</RouterLink>
      </div>
      <div class="foot">
        <LanguageSwitcher />
        <span class="mode-badge" :title="modeTitle">{{ modeLabel }}<span class="sr-only">. {{ modeTitle }}</span></span>
        <DropdownMenuRoot>
          <DropdownMenuTrigger class="account">
            <span class="avatar" aria-hidden="true">{{ initials }}</span>
            <span class="who">
              <span class="name">{{ session?.name }}</span>
              <span class="email">{{ session?.email }}</span>
            </span>
          </DropdownMenuTrigger>
          <DropdownMenuPortal>
            <DropdownMenuContent class="menu" side="top" align="start" :side-offset="8">
              <DropdownMenuLabel class="menu-label">{{ t('account.signedInAs') }}<strong>{{ session?.email }}</strong></DropdownMenuLabel>
              <DropdownMenuItem class="menu-item menu-item-danger" @select="signOut">{{ t('account.logout') }}</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenuPortal>
        </DropdownMenuRoot>
      </div>
    </nav>
    <main class="content">
      <!-- Keyed by path: moving between campaigns gives each its own page state and save queue. -->
      <RouterView :key="$route.path" />
    </main>
  </div>
</template>

<style scoped>
.shell {
  min-height: 100vh;
  display: grid;
  grid-template-columns: 240px minmax(0, 1fr);
}
/* Stays in view on long pages; scrolls itself on short screens. */
.sidebar {
  position: sticky;
  top: 0;
  height: 100vh;
  overflow-y: auto;
  background: var(--surface);
  border-right: 1px solid var(--line);
  padding: 22px 16px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.brand-name {
  max-width: 130px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 6px;
  font-weight: 650;
  line-height: 1.2;
}
.new {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 40px;
  border-radius: 9px;
  border: 1px solid #d4d1f6;
  background: #f6f5fe;
  color: var(--primary-ink);
  font-weight: 600;
  text-decoration: none;
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
  min-width: 0;
  padding: 24px 32px 40px;
}
/* Tablets and phones: the sidebar becomes a compact bar above the page. */
@media (max-width: 899px) {
  .shell {
    grid-template-columns: minmax(0, 1fr);
    align-content: start;
  }
  .sidebar {
    position: static;
    height: auto;
    overflow: visible;
    flex-direction: row;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 12px;
    padding: 10px 16px;
    border-right: 0;
    border-bottom: 1px solid var(--line);
  }
  .brand {
    padding: 0;
  }
  .new {
    order: 1;
    padding: 0 12px;
    height: 36px;
  }
  .links {
    flex-direction: row;
  }
  .link {
    padding: 8px 10px;
  }
  .foot {
    order: 2;
    margin-top: 0;
    margin-left: auto;
    flex-direction: row;
    align-items: center;
    gap: 10px;
  }
  .account {
    width: auto;
  }
  .content {
    padding: 20px 16px 32px;
  }
}
/* Phones: the avatar alone opens the account menu; the name stays for screen readers. */
@media (max-width: 640px) {
  .brand-name,
  .who {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
  }
  .foot {
    margin-left: 0;
  }
}
</style>
