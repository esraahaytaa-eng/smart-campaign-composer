<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../api'
import { safeRedirect } from '../guard'
import { focusTarget } from '../login'
import { login, sessionProblem } from '../session'
import BrandMark from '../components/BrandMark.vue'
import LanguageSwitcher from '../components/LanguageSwitcher.vue'
import { t } from '../i18n'
import { errorText } from '../i18n/errors'

const router = useRouter()
const route = useRoute()

const email = ref('')
const password = ref('')
const showPassword = ref(false)
const tried = ref(false)
const busy = ref(false)
// From the server: wrong login, or a problem reaching it (also on first load).
// Kept as the error, so it is shown in the current language.
const failure = ref<unknown>(sessionProblem.value)
const failureText = computed(() =>
  failure.value instanceof ApiError && failure.value.status === 401 ? t('auth.wrongLogin') : failure.value ? errorText(failure.value) : '',
)
const emailInput = ref<HTMLInputElement>()
const passwordInput = ref<HTMLInputElement>()
// The page has one job: start in the first field (also after signing out,
// when the focused menu item has just disappeared).
onMounted(() => emailInput.value?.focus())

const emailError = computed(() => {
  const v = email.value.trim()
  if (!v) return t('auth.emailRequired')
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v) ? '' : t('auth.emailInvalid')
})
const passwordError = computed(() => (password.value ? '' : t('auth.passwordRequired')))

async function focusProblem() {
  await nextTick()
  const field = focusTarget({ email: emailError.value, password: passwordError.value }, !!failure.value)
  if (field) (field === 'email' ? emailInput : passwordInput).value?.focus()
}

async function submit() {
  if (busy.value) return
  tried.value = true
  failure.value = null
  if (emailError.value || passwordError.value) return focusProblem()
  busy.value = true
  try {
    await login(email.value.trim(), password.value)
    await router.replace(safeRedirect(route.query.redirect) ?? '/')
  } catch (err) {
    failure.value = err
    busy.value = false
    await focusProblem()
  } finally {
    busy.value = false
  }
}

function clearFailure() {
  failure.value = null
}
</script>

<template>
  <div class="login">
    <section class="pitch" :aria-label="t('auth.about')">
      <div class="brand">
        <BrandMark inverted />
        <span>{{ t('app.name') }}</span>
      </div>
      <div class="pitch-body">
        <h1>{{ t('auth.headline') }}</h1>
        <ol>
          <li>{{ t('auth.point1') }}</li>
          <li>{{ t('auth.point2') }}</li>
          <li>{{ t('auth.point3') }}</li>
        </ol>
      </div>
      <!-- The AI mode is only known after sign-in (GET /api/me); providers are always mocked. -->
      <span class="pitch-badge">{{ t('auth.providers') }}</span>
    </section>

    <main class="form-side">
      <LanguageSwitcher class="lang" />
      <form novalidate aria-labelledby="login-title" @submit.prevent="submit">
        <div class="heading">
          <h2 id="login-title">{{ t('auth.signIn') }}</h2>
          <span>{{ t('auth.subtitle') }}</span>
        </div>

        <div v-if="failureText" role="alert" class="alert alert-error">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#a31f15" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 8v5M12 16h.01" /></svg>
          <span>{{ failureText }}</span>
        </div>

        <div class="field">
          <label for="email">{{ t('auth.email') }}</label>
          <input
            id="email"
            ref="emailInput"
            v-model="email"
            class="input"
            type="email"
            autocomplete="username"
            :placeholder="t('auth.emailPlaceholder')"
            :readonly="busy"
            :aria-invalid="tried && !!emailError"
            :aria-describedby="tried && emailError ? 'email-error' : undefined"
            @input="clearFailure"
          />
          <span v-if="tried && emailError" id="email-error" class="field-error">{{ emailError }}</span>
        </div>

        <div class="field">
          <label for="password">{{ t('auth.password') }}</label>
          <div class="password" :class="{ invalid: tried && !!passwordError }">
            <input
              id="password"
              ref="passwordInput"
              v-model="password"
              :type="showPassword ? 'text' : 'password'"
              autocomplete="current-password"
              :readonly="busy"
              :aria-invalid="tried && !!passwordError"
              :aria-describedby="tried && passwordError ? 'password-error' : undefined"
              @input="clearFailure"
            />
            <button type="button" class="reveal" @click="showPassword = !showPassword">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z" /><circle cx="12" cy="12" r="3" /></svg>
              {{ t(showPassword ? 'auth.hide' : 'auth.show') }}<span class="sr-only">{{ t('auth.revealSuffix') }}</span>
            </button>
          </div>
          <span v-if="tried && passwordError" id="password-error" class="field-error">{{ passwordError }}</span>
        </div>

        <!-- aria-disabled, not disabled: a disabled button would drop keyboard focus. -->
        <button type="submit" class="btn btn-primary btn-lg" :aria-disabled="busy" :class="{ busy }">
          <span v-if="busy" class="spinner" aria-hidden="true" />
          {{ t(busy ? 'auth.signingIn' : 'auth.signIn') }}
        </button>

        <p class="demo-note">{{ t('auth.demoNote') }}</p>
      </form>
    </main>
  </div>
</template>

<style scoped>
.login {
  min-height: 100vh;
  display: flex;
  flex-wrap: wrap;
}
.pitch {
  flex: 1 1 420px;
  background: var(--ink);
  color: #fff;
  padding: 48px;
  display: flex;
  flex-direction: column;
  gap: 40px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  font-weight: 650;
  font-size: 16px;
}
.pitch-body {
  margin-top: auto;
  max-width: 460px;
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.pitch-body h1 {
  font-size: 34px;
  line-height: 1.15;
  letter-spacing: -0.02em;
}
.pitch ol {
  margin: 0;
  padding: 0;
  list-style: none;
  counter-reset: step;
  display: flex;
  flex-direction: column;
  gap: 12px;
  font-size: 15px;
  color: #d9dcea;
}
.pitch li {
  counter-increment: step;
  display: flex;
  gap: 10px;
  align-items: flex-start;
}
.pitch li::before {
  content: counter(step);
  flex: 0 0 22px;
  height: 22px;
  border-radius: 999px;
  background: #2a3556;
  color: #c9ccf8;
  font-size: 12px;
  font-weight: 700;
  display: flex;
  align-items: center;
  justify-content: center;
}
.pitch-badge {
  margin-top: auto;
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 11px;
  border-radius: 999px;
  background: #2a3556;
  color: #f1d9ae;
  font-size: 12px;
  font-weight: 600;
}
.pitch-badge::before {
  content: '';
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: var(--notice-dot);
}
.form-side {
  position: relative;
  flex: 1 1 480px;
  padding: 48px 24px;
  display: flex;
  align-items: center;
  justify-content: center;
}
form {
  width: 100%;
  max-width: 400px;
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.lang {
  position: absolute;
  top: 24px;
  right: 24px;
}
.heading {
  display: flex;
  flex-direction: column;
  gap: 6px;
  color: var(--ink-2);
}
.heading h2 {
  margin: 0;
  font-size: 26px;
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--ink);
}
.password {
  display: flex;
  align-items: center;
  height: 46px;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius);
  background: var(--surface);
  padding-right: 4px;
}
.password.invalid {
  border: 1.5px solid var(--danger-line);
}
.password:focus-within {
  outline: var(--focus-ring);
  outline-offset: 2px;
}
.password input {
  flex: 1;
  min-width: 0;
  height: 100%;
  border: 0;
  outline: none;
  border-radius: var(--radius);
  padding: 0 14px;
  background: transparent;
  color: var(--ink);
  font: 400 15px var(--font);
}
.reveal {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 38px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--ink-2);
  font: 600 13px var(--font);
  cursor: pointer;
}
.demo-note {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
}
@media (max-width: 640px) {
  .pitch {
    padding: 32px 24px;
  }
}
</style>
