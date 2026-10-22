<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../api'
import { safeRedirect } from '../guard'
import { focusTarget } from '../login'
import { login, sessionProblem } from '../session'
import BrandMark from '../components/BrandMark.vue'

const router = useRouter()
const route = useRoute()

const email = ref('')
const password = ref('')
const showPassword = ref(false)
const tried = ref(false)
const busy = ref(false)
// From the server: wrong login, or a problem reaching it (also on first load).
const failure = ref(sessionProblem.value)
const emailInput = ref<HTMLInputElement>()
const passwordInput = ref<HTMLInputElement>()

const emailError = computed(() => {
  const v = email.value.trim()
  if (!v) return 'Enter your work email.'
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v) ? '' : 'Enter a valid email, like name@company.com.'
})
const passwordError = computed(() => (password.value ? '' : 'Enter your password.'))

async function focusProblem() {
  await nextTick()
  const field = focusTarget({ email: emailError.value, password: passwordError.value }, !!failure.value)
  if (field) (field === 'email' ? emailInput : passwordInput).value?.focus()
}

async function submit() {
  if (busy.value) return
  tried.value = true
  failure.value = ''
  if (emailError.value || passwordError.value) return focusProblem()
  busy.value = true
  try {
    await login(email.value.trim(), password.value)
    await router.replace(safeRedirect(route.query.redirect) ?? '/')
  } catch (err) {
    failure.value =
      err instanceof ApiError && err.status === 401
        ? 'Email or password is incorrect. Check both and try again.'
        : err instanceof ApiError
          ? err.message
          : 'Something went wrong. Try again.'
    busy.value = false
    await focusProblem()
  } finally {
    busy.value = false
  }
}

function clearFailure() {
  failure.value = ''
}
</script>

<template>
  <div class="login">
    <section class="pitch" aria-label="About Smart Campaign Composer">
      <div class="brand">
        <BrandMark inverted />
        <span>Smart Campaign Composer</span>
      </div>
      <div class="pitch-body">
        <h1>From a one-line brief to a campaign you approved.</h1>
        <ol>
          <li>AI turns your brief into audience rules you can edit</li>
          <li>SMS and email drafts, checked against character limits</li>
          <li>Nothing sends until you approve; every result is traceable</li>
        </ol>
      </div>
      <!-- The AI mode is only known after sign-in (GET /api/me); providers are always mocked. -->
      <span class="pitch-badge">Mock SMS and email providers — no real messages are sent</span>
    </section>

    <main class="form-side">
      <form novalidate aria-labelledby="login-title" @submit.prevent="submit">
        <div class="heading">
          <h2 id="login-title">Sign in</h2>
          <span>Use your marketing team account.</span>
        </div>

        <div v-if="failure" role="alert" class="alert alert-error">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#a31f15" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 8v5M12 16h.01" /></svg>
          <span>{{ failure }}</span>
        </div>

        <div class="field">
          <label for="email">Work email</label>
          <input
            id="email"
            ref="emailInput"
            v-model="email"
            class="input"
            type="email"
            autocomplete="username"
            placeholder="you@company.com"
            :readonly="busy"
            :aria-invalid="tried && !!emailError"
            :aria-describedby="tried && emailError ? 'email-error' : undefined"
            @input="clearFailure"
          />
          <span v-if="tried && emailError" id="email-error" class="field-error">{{ emailError }}</span>
        </div>

        <div class="field">
          <label for="password">Password</label>
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
              {{ showPassword ? 'Hide' : 'Show' }}<span class="sr-only"> password</span>
            </button>
          </div>
          <span v-if="tried && passwordError" id="password-error" class="field-error">{{ passwordError }}</span>
        </div>

        <!-- aria-disabled, not disabled: a disabled button would drop keyboard focus. -->
        <button type="submit" class="btn btn-primary btn-lg" :aria-disabled="busy" :class="{ busy }">
          <span v-if="busy" class="spinner" aria-hidden="true" />
          {{ busy ? 'Signing in…' : 'Sign in' }}
        </button>

        <p class="demo-note">Running the local demo? The demo login is described in the README.</p>
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
